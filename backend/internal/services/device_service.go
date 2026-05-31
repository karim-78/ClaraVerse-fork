package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"claraverse/internal/database"
	"claraverse/internal/models"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Collection names for device auth
const (
	CollectionDeviceAuthRequests = "device_auth_requests"
	CollectionUserDevices        = "user_devices"
	CollectionDeviceAuthAuditLog = "device_auth_audit_log"
)

// Configuration constants
const (
	DeviceCodeExpiry   = 5 * time.Minute      // Device code expires after 5 minutes
	MinPollInterval    = 5                     // Minimum polling interval in seconds
	SlowDownInterval   = 10                    // Interval after slow_down error
	MaxFailedAttempts  = 5                     // Max failed authorization attempts
	AccessTokenExpiry  = 5 * 24 * time.Hour    // Access token lifetime (5 days)
	RefreshTokenExpiry = 90 * 24 * time.Hour   // Refresh token lifetime (90 days)
)

// User code alphabet - excludes confusable characters (0/O, 1/I/L) and vowels (prevents offensive words)
const userCodeAlphabet = "BCDFGHJKMNPQRSTVWXYZ23456789"

var (
	ErrDeviceCodeExpired   = errors.New("device code has expired")
	ErrDeviceCodeNotFound  = errors.New("device code not found")
	ErrInvalidUserCode     = errors.New("invalid user code")
	ErrDeviceAlreadyAuth   = errors.New("device already authorized")
	ErrMaxAttemptsExceeded = errors.New("maximum authorization attempts exceeded")
	ErrPollingTooFast      = errors.New("polling too fast")
	ErrDeviceNotFound      = errors.New("device not found")
	ErrDeviceRevoked       = errors.New("device has been revoked")
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
)

// DeviceService handles device authentication operations
type DeviceService struct {
	mongodb   *database.MongoDB
	db        *database.DB // MySQL for fast revocation checks
	jwtSecret []byte
	appURL    string
}

// NewDeviceService creates a new device service
func NewDeviceService(mongodb *database.MongoDB, db *database.DB) *DeviceService {
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		// Generate a random secret for development
		log.Println("⚠️  JWT_SECRET not set, generating random secret (not suitable for production)")
		randomBytes := make([]byte, 32)
		rand.Read(randomBytes)
		jwtSecret = hex.EncodeToString(randomBytes)
	}

	appURL := os.Getenv("APP_URL")
	if appURL == "" {
		appURL = "http://localhost:3000"
	}

	return &DeviceService{
		mongodb:   mongodb,
		db:        db,
		jwtSecret: []byte(jwtSecret),
		appURL:    appURL,
	}
}

// InitializeIndexes creates MongoDB indexes for device auth collections
func (s *DeviceService) InitializeIndexes(ctx context.Context) error {
	// Device auth requests - TTL index for auto-expiry
	authReqCollection := s.mongodb.Collection(CollectionDeviceAuthRequests)
	_, err := authReqCollection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "deviceCode", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "userCode", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)}, // TTL index
		{Keys: bson.D{{Key: "status", Value: 1}}},
	})
	if err != nil {
		return fmt.Errorf("failed to create device_auth_requests indexes: %w", err)
	}

	// User devices
	devicesCollection := s.mongodb.Collection(CollectionUserDevices)
	_, err = devicesCollection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "isActive", Value: 1}}},
		{Keys: bson.D{{Key: "deviceId", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "refreshTokenHash", Value: 1}}},
	})
	if err != nil {
		return fmt.Errorf("failed to create user_devices indexes: %w", err)
	}

	// Audit log with TTL (90 days retention)
	auditCollection := s.mongodb.Collection(CollectionDeviceAuthAuditLog)
	_, err = auditCollection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "createdAt", Value: -1}}},
		{Keys: bson.D{{Key: "deviceId", Value: 1}}},
		{Keys: bson.D{{Key: "createdAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(90 * 24 * 3600)}, // 90 days TTL
	})
	if err != nil {
		return fmt.Errorf("failed to create device_auth_audit_log indexes: %w", err)
	}

	log.Println("✅ Device auth MongoDB indexes initialized")
	return nil
}

// GenerateDeviceCode creates a new device authorization request
func (s *DeviceService) GenerateDeviceCode(ctx context.Context, clientInfo models.DeviceClientInfo, ipAddress, userAgent string) (*models.DeviceCodeResponse, error) {
	// Generate cryptographically random device code (32 bytes = 64 hex chars)
	deviceCodeBytes := make([]byte, 32)
	if _, err := rand.Read(deviceCodeBytes); err != nil {
		return nil, fmt.Errorf("failed to generate device code: %w", err)
	}
	deviceCode := hex.EncodeToString(deviceCodeBytes)

	// Generate user-friendly code (8 chars, formatted as XXXX-XXXX)
	userCode := s.generateUserCode()

	now := time.Now()
	expiresAt := now.Add(DeviceCodeExpiry)

	authRequest := &models.DeviceAuthRequest{
		DeviceCode:     deviceCode,
		UserCode:       userCode,
		ClientInfo:     clientInfo,
		Status:         models.DeviceAuthStatusPending,
		IPAddress:      ipAddress,
		UserAgent:      userAgent,
		CreatedAt:      now,
		ExpiresAt:      expiresAt,
		PollCount:      0,
		FailedAttempts: 0,
	}

	collection := s.mongodb.Collection(CollectionDeviceAuthRequests)
	_, err := collection.InsertOne(ctx, authRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to store device auth request: %w", err)
	}

	// Log the event
	s.logAuditEvent(ctx, models.DeviceAuthEventCodeGenerated, "", "", map[string]interface{}{
		"device_code_prefix": deviceCode[:8],
		"user_code":          userCode,
		"platform":           clientInfo.Platform,
		"ip_address":         ipAddress,
	})

	// Build verification URLs
	verificationURI := s.appURL + "/device"
	verificationURIComplete := verificationURI + "?code=" + userCode

	return &models.DeviceCodeResponse{
		DeviceCode:              deviceCode,
		UserCode:                userCode,
		VerificationURI:         verificationURI,
		VerificationURIComplete: verificationURIComplete,
		ExpiresIn:               int(DeviceCodeExpiry.Seconds()),
		Interval:                MinPollInterval,
	}, nil
}

// generateUserCode creates a user-friendly 8-character code
func (s *DeviceService) generateUserCode() string {
	code := make([]byte, 8)
	alphabetLen := len(userCodeAlphabet)

	randomBytes := make([]byte, 8)
	rand.Read(randomBytes)

	for i := 0; i < 8; i++ {
		code[i] = userCodeAlphabet[int(randomBytes[i])%alphabetLen]
	}

	// Format as XXXX-XXXX
	return string(code[:4]) + "-" + string(code[4:])
}

// PollForToken checks if the device has been authorized and returns tokens
func (s *DeviceService) PollForToken(ctx context.Context, deviceCode, clientID string) (*models.DeviceTokenResponse, *models.DeviceTokenErrorResponse) {
	collection := s.mongodb.Collection(CollectionDeviceAuthRequests)

	var authRequest models.DeviceAuthRequest
	err := collection.FindOne(ctx, bson.M{"deviceCode": deviceCode}).Decode(&authRequest)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, &models.DeviceTokenErrorResponse{
				Error:            models.DeviceTokenErrorInvalidGrant,
				ErrorDescription: "Invalid device code.",
			}
		}
		log.Printf("Error finding device auth request: %v", err)
		return nil, &models.DeviceTokenErrorResponse{
			Error:            models.DeviceTokenErrorInvalidGrant,
			ErrorDescription: "Internal error.",
		}
	}

	// Check if expired
	if time.Now().After(authRequest.ExpiresAt) {
		return nil, &models.DeviceTokenErrorResponse{
			Error:            models.DeviceTokenErrorExpiredToken,
			ErrorDescription: "The device code has expired.",
		}
	}

	// Check polling rate
	if authRequest.LastPollAt != nil {
		timeSinceLastPoll := time.Since(*authRequest.LastPollAt)
		if timeSinceLastPoll < time.Duration(MinPollInterval)*time.Second {
			// Update poll count and return slow_down
			collection.UpdateOne(ctx, bson.M{"deviceCode": deviceCode}, bson.M{
				"$set": bson.M{"lastPollAt": time.Now()},
				"$inc": bson.M{"pollCount": 1},
			})
			return nil, &models.DeviceTokenErrorResponse{
				Error:            models.DeviceTokenErrorSlowDown,
				ErrorDescription: "Please slow down your polling rate.",
				Interval:         SlowDownInterval,
			}
		}
	}

	// Update last poll time
	now := time.Now()
	collection.UpdateOne(ctx, bson.M{"deviceCode": deviceCode}, bson.M{
		"$set": bson.M{"lastPollAt": now},
		"$inc": bson.M{"pollCount": 1},
	})

	// Check status
	switch authRequest.Status {
	case models.DeviceAuthStatusPending:
		return nil, &models.DeviceTokenErrorResponse{
			Error:            models.DeviceTokenErrorAuthorizationPending,
			ErrorDescription: "The user has not yet authorized this device.",
		}

	case models.DeviceAuthStatusDenied:
		return nil, &models.DeviceTokenErrorResponse{
			Error:            models.DeviceTokenErrorAccessDenied,
			ErrorDescription: "The user denied the authorization request.",
		}

	case models.DeviceAuthStatusExpired:
		return nil, &models.DeviceTokenErrorResponse{
			Error:            models.DeviceTokenErrorExpiredToken,
			ErrorDescription: "The device code has expired.",
		}

	case models.DeviceAuthStatusAuthorized:
		// Generate tokens and create device record
		return s.completeAuthorization(ctx, &authRequest)
	}

	return nil, &models.DeviceTokenErrorResponse{
		Error:            models.DeviceTokenErrorInvalidGrant,
		ErrorDescription: "Unknown authorization status.",
	}
}

// completeAuthorization creates the device record and issues tokens
func (s *DeviceService) completeAuthorization(ctx context.Context, authRequest *models.DeviceAuthRequest) (*models.DeviceTokenResponse, *models.DeviceTokenErrorResponse) {
	// Generate device ID
	deviceID := uuid.New().String()

	// Generate refresh token
	refreshTokenBytes := make([]byte, 48)
	rand.Read(refreshTokenBytes)
	refreshToken := base64.URLEncoding.EncodeToString(refreshTokenBytes)

	// Hash refresh token for storage
	refreshTokenHash := s.hashToken(refreshToken)

	// Generate access token (JWT)
	accessToken, err := s.generateAccessToken(authRequest.UserID, deviceID)
	if err != nil {
		log.Printf("Error generating access token: %v", err)
		return nil, &models.DeviceTokenErrorResponse{
			Error:            models.DeviceTokenErrorInvalidGrant,
			ErrorDescription: "Failed to generate access token.",
		}
	}

	now := time.Now()

	// Create device record
	device := &models.UserDevice{
		UserID:           authRequest.UserID,
		DeviceID:         deviceID,
		Name:             s.generateDeviceName(authRequest.ClientInfo.Platform),
		Platform:         authRequest.ClientInfo.Platform,
		ClientVersion:    authRequest.ClientInfo.Version,
		RefreshTokenHash: refreshTokenHash,
		IsActive:         true,
		LastActiveAt:     now,
		LastIPAddress:    authRequest.IPAddress,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	devicesCollection := s.mongodb.Collection(CollectionUserDevices)
	_, err = devicesCollection.InsertOne(ctx, device)
	if err != nil {
		log.Printf("Error creating device record: %v", err)
		return nil, &models.DeviceTokenErrorResponse{
			Error:            models.DeviceTokenErrorInvalidGrant,
			ErrorDescription: "Failed to create device record.",
		}
	}

	// Delete the auth request (one-time use)
	authCollection := s.mongodb.Collection(CollectionDeviceAuthRequests)
	authCollection.DeleteOne(ctx, bson.M{"deviceCode": authRequest.DeviceCode})

	// Add to MySQL for fast revocation checks
	if s.db != nil {
		tokenHash := s.hashToken(accessToken[:min(32, len(accessToken))])
		expiresAt := now.Add(AccessTokenExpiry)
		_, err = s.db.Exec(`
			INSERT INTO device_tokens (device_id, user_id, token_hash, is_revoked, expires_at, created_at)
			VALUES (?, ?, ?, FALSE, ?, ?)
			ON DUPLICATE KEY UPDATE token_hash = VALUES(token_hash), expires_at = VALUES(expires_at), is_revoked = FALSE
		`, deviceID, authRequest.UserID, tokenHash, expiresAt, now)
		if err != nil {
			log.Printf("Warning: Failed to add device token to MySQL: %v", err)
		}
	}

	// Log the event
	s.logAuditEvent(ctx, models.DeviceAuthEventAuthorized, authRequest.UserID, deviceID, map[string]interface{}{
		"platform":   authRequest.ClientInfo.Platform,
		"ip_address": authRequest.IPAddress,
	})

	return &models.DeviceTokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(AccessTokenExpiry.Seconds()),
		RefreshToken: refreshToken,
		DeviceID:     deviceID,
		User: struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		}{
			ID:    authRequest.UserID,
			Email: authRequest.UserEmail,
		},
	}, nil
}

// AuthorizeDevice is called when user enters the code in the browser
func (s *DeviceService) AuthorizeDevice(ctx context.Context, userCode, userID, userEmail string) (*models.DeviceAuthorizeResponse, error) {
	// Normalize user code (remove hyphen, uppercase)
	userCode = strings.ToUpper(strings.ReplaceAll(userCode, "-", ""))
	formattedCode := userCode[:4] + "-" + userCode[4:]

	collection := s.mongodb.Collection(CollectionDeviceAuthRequests)

	var authRequest models.DeviceAuthRequest
	err := collection.FindOne(ctx, bson.M{
		"userCode": formattedCode,
		"status":   models.DeviceAuthStatusPending,
	}).Decode(&authRequest)

	if err != nil {
		if err == mongo.ErrNoDocuments {
			// Log failed attempt
			s.logAuditEvent(ctx, models.DeviceAuthEventLoginFailed, userID, "", map[string]interface{}{
				"user_code_attempted": formattedCode,
				"reason":              "invalid_code",
			})
			return nil, ErrInvalidUserCode
		}
		return nil, fmt.Errorf("database error: %w", err)
	}

	// Check if expired
	if time.Now().After(authRequest.ExpiresAt) {
		return nil, ErrDeviceCodeExpired
	}

	// Check if max attempts exceeded
	if authRequest.FailedAttempts >= MaxFailedAttempts {
		return nil, ErrMaxAttemptsExceeded
	}

	// Mark as authorized
	now := time.Now()
	_, err = collection.UpdateOne(ctx, bson.M{"deviceCode": authRequest.DeviceCode}, bson.M{
		"$set": bson.M{
			"status":       models.DeviceAuthStatusAuthorized,
			"userId":       userID,
			"userEmail":    userEmail,
			"authorizedAt": now,
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to authorize device: %w", err)
	}

	// Log the event
	s.logAuditEvent(ctx, models.DeviceAuthEventCodeEntered, userID, "", map[string]interface{}{
		"user_code": formattedCode,
		"platform":  authRequest.ClientInfo.Platform,
		"success":   true,
	})

	return &models.DeviceAuthorizeResponse{
		Success:    true,
		DeviceInfo: &authRequest.ClientInfo,
	}, nil
}

// RefreshAccessToken refreshes an access token using a refresh token
func (s *DeviceService) RefreshAccessToken(ctx context.Context, refreshToken, deviceID string) (*models.DeviceTokenResponse, error) {
	refreshTokenHash := s.hashToken(refreshToken)

	collection := s.mongodb.Collection(CollectionUserDevices)

	var device models.UserDevice
	err := collection.FindOne(ctx, bson.M{
		"deviceId":         deviceID,
		"refreshTokenHash": refreshTokenHash,
	}).Decode(&device)

	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrInvalidRefreshToken
		}
		return nil, fmt.Errorf("database error: %w", err)
	}

	// Check if device is active
	if !device.IsActive {
		return nil, ErrDeviceRevoked
	}

	if device.RevokedAt != nil {
		return nil, ErrDeviceRevoked
	}

	// Generate new access token
	accessToken, err := s.generateAccessToken(device.UserID, deviceID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate access token: %w", err)
	}

	// Generate new refresh token (rotation)
	newRefreshTokenBytes := make([]byte, 48)
	rand.Read(newRefreshTokenBytes)
	newRefreshToken := base64.URLEncoding.EncodeToString(newRefreshTokenBytes)
	newRefreshTokenHash := s.hashToken(newRefreshToken)

	// Update device with new refresh token and activity
	now := time.Now()
	_, err = collection.UpdateOne(ctx, bson.M{"deviceId": deviceID}, bson.M{
		"$set": bson.M{
			"refreshTokenHash": newRefreshTokenHash,
			"lastActiveAt":     now,
			"updatedAt":        now,
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to update device: %w", err)
	}

	// Update MySQL cache
	if s.db != nil {
		tokenHash := s.hashToken(accessToken[:min(32, len(accessToken))])
		expiresAt := now.Add(AccessTokenExpiry)
		s.db.Exec(`
			UPDATE device_tokens SET token_hash = ?, expires_at = ?, is_revoked = FALSE
			WHERE device_id = ?
		`, tokenHash, expiresAt, deviceID)
	}

	// Log the event
	s.logAuditEvent(ctx, models.DeviceAuthEventTokenRefreshed, device.UserID, deviceID, nil)

	// Get user email
	userEmail := ""
	var user struct {
		Email string `bson:"email"`
	}
	s.mongodb.Collection("users").FindOne(ctx, bson.M{"userId": device.UserID}).Decode(&user)
	userEmail = user.Email

	return &models.DeviceTokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(AccessTokenExpiry.Seconds()),
		RefreshToken: newRefreshToken,
		DeviceID:     deviceID,
		User: struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		}{
			ID:    device.UserID,
			Email: userEmail,
		},
	}, nil
}

// ListUserDevices returns all devices for a user
func (s *DeviceService) ListUserDevices(ctx context.Context, userID string, currentDeviceID string) ([]models.UserDeviceInfo, error) {
	collection := s.mongodb.Collection(CollectionUserDevices)

	cursor, err := collection.Find(ctx, bson.M{
		"userId":   userID,
		"isActive": true,
	}, options.Find().SetSort(bson.D{{Key: "lastActiveAt", Value: -1}}))

	if err != nil {
		return nil, fmt.Errorf("failed to list devices: %w", err)
	}
	defer cursor.Close(ctx)

	var devices []models.UserDeviceInfo
	for cursor.Next(ctx) {
		var device models.UserDevice
		if err := cursor.Decode(&device); err != nil {
			continue
		}

		devices = append(devices, models.UserDeviceInfo{
			ID:            device.ID,
			DeviceID:      device.DeviceID,
			Name:          device.Name,
			Platform:      device.Platform,
			ClientVersion: device.ClientVersion,
			IsActive:      device.IsActive,
			IsCurrent:     device.DeviceID == currentDeviceID,
			LastActiveAt:  device.LastActiveAt,
			LastIPAddress: device.LastIPAddress,
			LastLocation:  device.LastLocation,
			CreatedAt:     device.CreatedAt,
		})
	}

	return devices, nil
}

// UpdateDevice updates a device's info (like name)
func (s *DeviceService) UpdateDevice(ctx context.Context, userID, deviceID, name string) error {
	collection := s.mongodb.Collection(CollectionUserDevices)

	result, err := collection.UpdateOne(ctx, bson.M{
		"userId":   userID,
		"deviceId": deviceID,
	}, bson.M{
		"$set": bson.M{
			"name":      name,
			"updatedAt": time.Now(),
		},
	})

	if err != nil {
		return fmt.Errorf("failed to update device: %w", err)
	}

	if result.MatchedCount == 0 {
		return ErrDeviceNotFound
	}

	return nil
}

// RevokeDevice revokes a device's access
func (s *DeviceService) RevokeDevice(ctx context.Context, userID, deviceID string) error {
	collection := s.mongodb.Collection(CollectionUserDevices)

	now := time.Now()
	result, err := collection.UpdateOne(ctx, bson.M{
		"userId":   userID,
		"deviceId": deviceID,
	}, bson.M{
		"$set": bson.M{
			"isActive":  false,
			"revokedAt": now,
			"updatedAt": now,
		},
	})

	if err != nil {
		return fmt.Errorf("failed to revoke device: %w", err)
	}

	if result.MatchedCount == 0 {
		return ErrDeviceNotFound
	}

	// Mark as revoked in MySQL for fast checks
	if s.db != nil {
		s.db.Exec(`UPDATE device_tokens SET is_revoked = TRUE, revoked_at = ? WHERE device_id = ?`, now, deviceID)
	}

	// Log the event
	s.logAuditEvent(ctx, models.DeviceAuthEventRevoked, userID, deviceID, nil)

	return nil
}

// ValidateDeviceToken validates a device JWT token
func (s *DeviceService) ValidateDeviceToken(tokenString string) (*DeviceTokenClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &DeviceTokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.jwtSecret, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	claims, ok := token.Claims.(*DeviceTokenClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}

	// Check if token type is "device"
	if claims.Type != "device" {
		return nil, errors.New("not a device token")
	}

	return claims, nil
}

// IsDeviceRevoked checks if a device has been revoked (fast check via MySQL)
func (s *DeviceService) IsDeviceRevoked(ctx context.Context, deviceID string) (bool, error) {
	if s.db == nil {
		// Fall back to MongoDB check
		collection := s.mongodb.Collection(CollectionUserDevices)
		var device models.UserDevice
		err := collection.FindOne(ctx, bson.M{"deviceId": deviceID}).Decode(&device)
		if err != nil {
			if err == mongo.ErrNoDocuments {
				return true, nil // Not found = revoked
			}
			return false, err
		}
		return !device.IsActive || device.RevokedAt != nil, nil
	}

	var isRevoked bool
	err := s.db.QueryRow(`SELECT is_revoked FROM device_tokens WHERE device_id = ?`, deviceID).Scan(&isRevoked)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil // Not found = revoked
		}
		return false, err
	}
	return isRevoked, nil
}

// generateAccessToken creates a JWT access token for a device
func (s *DeviceService) generateAccessToken(userID, deviceID string) (string, error) {
	now := time.Now()
	claims := &DeviceTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "claraverse",
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
		DeviceID: deviceID,
		Type:     "device",
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

// hashToken creates a SHA-256 hash of a token
func (s *DeviceService) hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// generateDeviceName creates a default name based on platform
func (s *DeviceService) generateDeviceName(platform string) string {
	switch platform {
	case "darwin":
		return "Mac"
	case "linux":
		return "Linux Machine"
	case "windows":
		return "Windows PC"
	default:
		return "Unknown Device"
	}
}

// logAuditEvent logs a device auth event
func (s *DeviceService) logAuditEvent(ctx context.Context, eventType, userID, deviceID string, metadata map[string]interface{}) {
	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	auditLog := &models.DeviceAuthAuditLog{
		EventType: eventType,
		UserID:    userID,
		DeviceID:  deviceID,
		Metadata:  metadata,
		CreatedAt: time.Now(),
	}

	collection := s.mongodb.Collection(CollectionDeviceAuthAuditLog)
	_, err := collection.InsertOne(ctx, auditLog)
	if err != nil {
		log.Printf("Warning: Failed to log device auth event: %v", err)
	}
}

// DeviceTokenClaims represents the claims in a device JWT
type DeviceTokenClaims struct {
	jwt.RegisteredClaims
	DeviceID string `json:"device_id"`
	Type     string `json:"type"` // "device"
}
