package middleware

import (
	"claraverse/internal/services"
	"claraverse/pkg/auth"
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// deviceServiceInstance is the shared device service for middleware use
var deviceServiceInstance *services.DeviceService

// SetDeviceService sets the device service for middleware device token validation
func SetDeviceService(ds *services.DeviceService) {
	deviceServiceInstance = ds
}

// LocalAuthMiddleware verifies local JWT tokens
// Supports both Authorization header and query parameter (for WebSocket connections)
func LocalAuthMiddleware(jwtAuth *auth.LocalJWTAuth) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Skip auth if JWT secret is not configured (development mode ONLY)
		environment := os.Getenv("ENVIRONMENT")

		if jwtAuth == nil {
			// CRITICAL: Never allow auth bypass in production
			if environment == "production" {
				log.Fatal("❌ CRITICAL SECURITY ERROR: JWT auth not configured in production environment. Authentication is required.")
			}

			// Only allow bypass in development/testing
			if environment != "development" && environment != "testing" && environment != "" {
				return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
					"error": "Authentication service unavailable",
				})
			}

			log.Println("⚠️  Auth skipped: JWT not configured (development mode)")
			c.Locals("user_id", "dev-user")
			c.Locals("user_email", "dev@localhost")
			c.Locals("user_role", "user")
			return c.Next()
		}

		// Try to extract token from multiple sources
		var token string

		// 1. Try Authorization header first
		authHeader := c.Get("Authorization")
		if authHeader != "" {
			extractedToken, err := auth.ExtractToken(authHeader)
			if err == nil {
				token = extractedToken
			}
		}

		// 2. Try query parameter (for WebSocket connections)
		if token == "" {
			token = c.Query("token")
		}

		// No token found
		if token == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Missing or invalid authorization token",
			})
		}

		// Try device token first (if device service is available and token looks like a device JWT)
		if deviceServiceInstance != nil && isDeviceToken(token) {
			claims, err := deviceServiceInstance.ValidateDeviceToken(token)
			if err == nil {
				// Check if device is revoked
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				revoked, err := deviceServiceInstance.IsDeviceRevoked(ctx, claims.DeviceID)
				if err != nil {
					log.Printf("Error checking device revocation: %v", err)
				} else if revoked {
					return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
						"error": "Device has been revoked",
					})
				}

				// Store device user info in context
				c.Locals("user_id", claims.Subject)
				c.Locals("user_role", "authenticated")
				c.Locals("device_id", claims.DeviceID)
				c.Locals("device_claims", claims)
				c.Locals("auth_type", "device")

				return c.Next()
			}
			// If device token validation fails, fall through to local JWT validation
		}

		// Verify local JWT token
		user, err := jwtAuth.VerifyAccessToken(token)
		if err != nil {
			log.Printf("❌ Auth failed: %v", err)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Invalid or expired token",
			})
		}

		// Store user info in context
		c.Locals("user_id", user.ID)
		c.Locals("user_email", user.Email)
		c.Locals("user_role", user.Role)
		c.Locals("auth_type", "local")

		return c.Next()
	}
}

// OptionalLocalAuthMiddleware makes authentication optional
// Supports both Authorization header and query parameter (for WebSocket)
func OptionalLocalAuthMiddleware(jwtAuth *auth.LocalJWTAuth) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Try to extract token from multiple sources
		var token string

		// 1. Try Authorization header first
		authHeader := c.Get("Authorization")
		if authHeader != "" {
			extractedToken, err := auth.ExtractToken(authHeader)
			if err == nil {
				token = extractedToken
			}
		}

		// 2. Try query parameter (for WebSocket connections)
		if token == "" {
			token = c.Query("token")
		}

		// If no token found, proceed as anonymous
		if token == "" {
			c.Locals("user_id", "anonymous")
			return c.Next()
		}

		// Skip validation if JWT auth is not configured (development mode ONLY)
		environment := os.Getenv("ENVIRONMENT")

		if jwtAuth == nil {
			// CRITICAL: Never allow auth bypass in production
			if environment == "production" {
				log.Fatal("❌ CRITICAL SECURITY ERROR: JWT auth not configured in production environment")
			}

			// Only allow in development/testing
			if environment != "development" && environment != "testing" && environment != "" {
				c.Locals("user_id", "anonymous")
				return c.Next()
			}

			c.Locals("user_id", "dev-user")
			c.Locals("user_email", "dev@localhost")
			c.Locals("user_role", "user")
			return c.Next()
		}

		// Try device token first (if device service is available and token looks like a device JWT)
		if deviceServiceInstance != nil && isDeviceToken(token) {
			claims, err := deviceServiceInstance.ValidateDeviceToken(token)
			if err == nil {
				// Check if device is revoked
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				revoked, err := deviceServiceInstance.IsDeviceRevoked(ctx, claims.DeviceID)
				if err != nil {
					log.Printf("Error checking device revocation: %v", err)
				} else if revoked {
					c.Locals("user_id", "anonymous")
					return c.Next()
				}

				// Store device user info in context
				c.Locals("user_id", claims.Subject)
				c.Locals("user_role", "authenticated")
				c.Locals("device_id", claims.DeviceID)
				c.Locals("device_claims", claims)
				c.Locals("auth_type", "device")

				return c.Next()
			}
			// If device token validation fails, fall through to local JWT validation
		}

		// Verify local JWT token
		user, err := jwtAuth.VerifyAccessToken(token)
		if err != nil {
			c.Locals("user_id", "anonymous")
			return c.Next()
		}

		// Store authenticated user info
		c.Locals("user_id", user.ID)
		c.Locals("user_email", user.Email)
		c.Locals("user_role", user.Role)
		c.Locals("auth_type", "local")

		return c.Next()
	}
}

// isDeviceToken checks if a token looks like a ClaraVerse device JWT
// Device tokens are JWTs with "claraverse" issuer
func isDeviceToken(token string) bool {
	// JWTs have 3 parts separated by dots
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	// Quick check: both header and payload start with base64-encoded JSON (eyJ prefix)
	return strings.HasPrefix(parts[0], "eyJ") && strings.HasPrefix(parts[1], "eyJ")
}

// RateLimitedAuthMiddleware combines rate limiting with authentication
// Rate limit: 5 attempts per 15 minutes per IP
// Note: This function is currently unused. Apply rate limiting separately in routes if needed.
