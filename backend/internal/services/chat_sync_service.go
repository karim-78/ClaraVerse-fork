package services

import (
	"bytes"
	"claraverse/internal/crypto"
	"claraverse/internal/database"
	"claraverse/internal/models"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ChatSyncService handles cloud sync operations for chats with encryption
type ChatSyncService struct {
	db                *database.MongoDB
	collection        *mongo.Collection
	encryptionService *crypto.EncryptionService
}

// NewChatSyncService creates a new chat sync service
func NewChatSyncService(db *database.MongoDB, encryptionService *crypto.EncryptionService) *ChatSyncService {
	return &ChatSyncService{
		db:                db,
		collection:        db.Collection(database.CollectionChats),
		encryptionService: encryptionService,
	}
}

// CreateOrUpdateChat creates a new chat or updates an existing one
// Uses atomic upsert to prevent race conditions when multiple syncs arrive simultaneously
func (s *ChatSyncService) CreateOrUpdateChat(ctx context.Context, userID string, req *models.CreateChatRequest) (*models.ChatResponse, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID is required")
	}
	if req.ID == "" {
		return nil, fmt.Errorf("chat ID is required")
	}

	// Encrypt messages
	messagesJSON, err := json.Marshal(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize messages: %w", err)
	}

	encryptedMessages, err := s.encryptionService.Encrypt(userID, messagesJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt messages: %w", err)
	}

	// Compress encrypted messages to reduce storage size (helps avoid MongoDB 16MB limit)
	compressedMessages, err := s.compressData(encryptedMessages)
	if err != nil {
		return nil, fmt.Errorf("failed to compress messages: %w", err)
	}

	now := time.Now()

	filter := bson.M{
		"userId": userID,
		"chatId": req.ID,
	}

	// Use atomic upsert to handle race conditions
	// $setOnInsert only applies when creating a new document
	// $set applies to both insert and update
	// Note: Cannot use $setOnInsert and $inc on the same field (version),
	// so we set version to 1 on insert via $setOnInsert, and increment for updates via $inc
	update := bson.M{
		"$set": bson.M{
			"title":             req.Title,
			"encryptedMessages": compressedMessages,
			"isStarred":         req.IsStarred,
			"model":             req.Model,
			"updatedAt":         now,
		},
		"$setOnInsert": bson.M{
			"userId":    userID,
			"chatId":    req.ID,
			"createdAt": now,
		},
		"$inc": bson.M{
			"version": 1,
		},
	}

	opts := options.FindOneAndUpdate().
		SetUpsert(true).
		SetReturnDocument(options.After)

	var resultChat models.EncryptedChat
	err = s.collection.FindOneAndUpdate(ctx, filter, update, opts).Decode(&resultChat)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert chat: %w", err)
	}

	return &models.ChatResponse{
		ID:        req.ID,
		Title:     resultChat.Title,
		Messages:  req.Messages,
		IsStarred: resultChat.IsStarred,
		Model:     resultChat.Model,
		Version:   resultChat.Version,
		CreatedAt: resultChat.CreatedAt,
		UpdatedAt: resultChat.UpdatedAt,
	}, nil
}

// GetChat retrieves and decrypts a single chat
func (s *ChatSyncService) GetChat(ctx context.Context, userID, chatID string) (*models.ChatResponse, error) {
	if userID == "" || chatID == "" {
		return nil, fmt.Errorf("user ID and chat ID are required")
	}

	filter := bson.M{
		"userId": userID,
		"chatId": chatID,
	}

	var chat models.EncryptedChat
	err := s.collection.FindOne(ctx, filter).Decode(&chat)
	if err == mongo.ErrNoDocuments {
		return nil, fmt.Errorf("chat not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get chat: %w", err)
	}

	// Decrypt messages
	messages, err := s.decryptMessages(userID, chat.EncryptedMessages)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt messages: %w", err)
	}

	return &models.ChatResponse{
		ID:        chat.ChatID,
		Title:     chat.Title,
		Messages:  messages,
		IsStarred: chat.IsStarred,
		Model:     chat.Model,
		Version:   chat.Version,
		CreatedAt: chat.CreatedAt,
		UpdatedAt: chat.UpdatedAt,
	}, nil
}

// ListChats returns a paginated list of chats (metadata only, no messages)
func (s *ChatSyncService) ListChats(ctx context.Context, userID string, page, pageSize int, starredOnly bool) (*models.ChatListResponse, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID is required")
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	filter := bson.M{"userId": userID}
	if starredOnly {
		filter["isStarred"] = true
	}

	// Get total count
	totalCount, err := s.collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to count chats: %w", err)
	}

	// Find chats with pagination
	skip := int64((page - 1) * pageSize)
	opts := options.Find().
		SetSort(bson.D{{Key: "updatedAt", Value: -1}}).
		SetSkip(skip).
		SetLimit(int64(pageSize)).
		SetProjection(bson.M{
			"_id":               1,
			"chatId":            1,
			"title":             1,
			"isStarred":         1,
			"model":             1,
			"version":           1,
			"createdAt":         1,
			"updatedAt":         1,
			"encryptedMessages": 1, // Need this to count messages
		})

	cursor, err := s.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list chats: %w", err)
	}
	defer cursor.Close(ctx)

	var chats []models.ChatListItem
	for cursor.Next(ctx) {
		var encChat models.EncryptedChat
		if err := cursor.Decode(&encChat); err != nil {
			log.Printf("⚠️ Failed to decode chat: %v", err)
			continue
		}

		// Count messages (decrypt to get count)
		messageCount := 0
		if encChat.EncryptedMessages != "" {
			messages, err := s.decryptMessages(userID, encChat.EncryptedMessages)
			if err == nil {
				messageCount = len(messages)
			}
		}

		chats = append(chats, models.ChatListItem{
			ID:           encChat.ChatID,
			Title:        encChat.Title,
			IsStarred:    encChat.IsStarred,
			Model:        encChat.Model,
			MessageCount: messageCount,
			Version:      encChat.Version,
			CreatedAt:    encChat.CreatedAt,
			UpdatedAt:    encChat.UpdatedAt,
		})
	}

	return &models.ChatListResponse{
		Chats:      chats,
		TotalCount: totalCount,
		Page:       page,
		PageSize:   pageSize,
		HasMore:    int64(page*pageSize) < totalCount,
	}, nil
}

// UpdateChat performs a partial update on a chat
func (s *ChatSyncService) UpdateChat(ctx context.Context, userID, chatID string, req *models.UpdateChatRequest) (*models.ChatListItem, error) {
	if userID == "" || chatID == "" {
		return nil, fmt.Errorf("user ID and chat ID are required")
	}

	filter := bson.M{
		"userId":  userID,
		"chatId":  chatID,
		"version": req.Version, // Optimistic locking
	}

	updateFields := bson.M{
		"updatedAt": time.Now(),
	}

	if req.Title != nil {
		updateFields["title"] = *req.Title
	}
	if req.IsStarred != nil {
		updateFields["isStarred"] = *req.IsStarred
	}
	if req.Model != nil {
		updateFields["model"] = *req.Model
	}

	update := bson.M{
		"$set": updateFields,
		"$inc": bson.M{"version": 1},
	}

	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var updatedChat models.EncryptedChat
	err := s.collection.FindOneAndUpdate(ctx, filter, update, opts).Decode(&updatedChat)
	if err == mongo.ErrNoDocuments {
		return nil, fmt.Errorf("chat not found or version conflict")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update chat: %w", err)
	}

	// Count messages
	messageCount := 0
	if updatedChat.EncryptedMessages != "" {
		messages, err := s.decryptMessages(userID, updatedChat.EncryptedMessages)
		if err == nil {
			messageCount = len(messages)
		}
	}

	return &models.ChatListItem{
		ID:           updatedChat.ChatID,
		Title:        updatedChat.Title,
		IsStarred:    updatedChat.IsStarred,
		Model:        updatedChat.Model,
		MessageCount: messageCount,
		Version:      updatedChat.Version,
		CreatedAt:    updatedChat.CreatedAt,
		UpdatedAt:    updatedChat.UpdatedAt,
	}, nil
}

// DeleteChat removes a chat
func (s *ChatSyncService) DeleteChat(ctx context.Context, userID, chatID string) error {
	if userID == "" || chatID == "" {
		return fmt.Errorf("user ID and chat ID are required")
	}

	filter := bson.M{
		"userId": userID,
		"chatId": chatID,
	}

	result, err := s.collection.DeleteOne(ctx, filter)
	if err != nil {
		return fmt.Errorf("failed to delete chat: %w", err)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("chat not found")
	}

	return nil
}

// BulkSync uploads multiple chats at once
func (s *ChatSyncService) BulkSync(ctx context.Context, userID string, req *models.BulkSyncRequest) (*models.BulkSyncResponse, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID is required")
	}

	response := &models.BulkSyncResponse{
		ChatIDs: make([]string, 0),
	}

	for _, chatReq := range req.Chats {
		_, err := s.CreateOrUpdateChat(ctx, userID, &chatReq)
		if err != nil {
			response.Failed++
			response.Errors = append(response.Errors, fmt.Sprintf("chat %s: %v", chatReq.ID, err))
			log.Printf("⚠️ Failed to sync chat %s: %v", chatReq.ID, err)
		} else {
			response.Synced++
			response.ChatIDs = append(response.ChatIDs, chatReq.ID)
		}
	}

	return response, nil
}

// GetAllChats returns all chats for initial sync (with decrypted messages)
func (s *ChatSyncService) GetAllChats(ctx context.Context, userID string) (*models.SyncAllResponse, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID is required")
	}

	filter := bson.M{"userId": userID}
	opts := options.Find().SetSort(bson.D{{Key: "updatedAt", Value: -1}})

	cursor, err := s.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get chats: %w", err)
	}
	defer cursor.Close(ctx)

	chats := make([]models.ChatResponse, 0) // Initialize empty slice to avoid null in JSON
	for cursor.Next(ctx) {
		var encChat models.EncryptedChat
		if err := cursor.Decode(&encChat); err != nil {
			log.Printf("⚠️ Failed to decode chat: %v", err)
			continue
		}

		// Decrypt messages
		messages, err := s.decryptMessages(userID, encChat.EncryptedMessages)
		if err != nil {
			log.Printf("⚠️ Failed to decrypt messages for chat %s: %v", encChat.ChatID, err)
			continue
		}

		chats = append(chats, models.ChatResponse{
			ID:        encChat.ChatID,
			Title:     encChat.Title,
			Messages:  messages,
			IsStarred: encChat.IsStarred,
			Model:     encChat.Model,
			Version:   encChat.Version,
			CreatedAt: encChat.CreatedAt,
			UpdatedAt: encChat.UpdatedAt,
		})
	}

	return &models.SyncAllResponse{
		Chats:      chats,
		TotalCount: len(chats),
		SyncedAt:   time.Now(),
	}, nil
}

// AddMessage adds a single message to a chat with optimistic locking
func (s *ChatSyncService) AddMessage(ctx context.Context, userID, chatID string, req *models.ChatAddMessageRequest) (*models.ChatResponse, error) {
	if userID == "" || chatID == "" {
		return nil, fmt.Errorf("user ID and chat ID are required")
	}

	// Get current chat
	filter := bson.M{
		"userId":  userID,
		"chatId":  chatID,
		"version": req.Version, // Optimistic locking
	}

	var chat models.EncryptedChat
	err := s.collection.FindOne(ctx, filter).Decode(&chat)
	if err == mongo.ErrNoDocuments {
		return nil, fmt.Errorf("chat not found or version conflict")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get chat: %w", err)
	}

	// Decrypt existing messages
	messages, err := s.decryptMessages(userID, chat.EncryptedMessages)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt messages: %w", err)
	}

	// Add new message
	messages = append(messages, req.Message)

	// Re-encrypt messages
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize messages: %w", err)
	}

	encryptedMessages, err := s.encryptionService.Encrypt(userID, messagesJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt messages: %w", err)
	}

	// Compress encrypted messages to reduce storage size
	compressedMessages, err := s.compressData(encryptedMessages)
	if err != nil {
		return nil, fmt.Errorf("failed to compress messages: %w", err)
	}

	// Update chat
	now := time.Now()
	update := bson.M{
		"$set": bson.M{
			"encryptedMessages": compressedMessages,
			"updatedAt":         now,
		},
		"$inc": bson.M{"version": 1},
	}

	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var updatedChat models.EncryptedChat
	err = s.collection.FindOneAndUpdate(ctx, filter, update, opts).Decode(&updatedChat)
	if err == mongo.ErrNoDocuments {
		return nil, fmt.Errorf("version conflict during update")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update chat: %w", err)
	}

	return &models.ChatResponse{
		ID:        chatID,
		Title:     updatedChat.Title,
		Messages:  messages,
		IsStarred: updatedChat.IsStarred,
		Model:     updatedChat.Model,
		Version:   updatedChat.Version,
		CreatedAt: updatedChat.CreatedAt,
		UpdatedAt: updatedChat.UpdatedAt,
	}, nil
}

// DeleteAllUserChats removes all chats for a user (GDPR compliance)
func (s *ChatSyncService) DeleteAllUserChats(ctx context.Context, userID string) (int64, error) {
	if userID == "" {
		return 0, fmt.Errorf("user ID is required")
	}

	filter := bson.M{"userId": userID}
	result, err := s.collection.DeleteMany(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("failed to delete user chats: %w", err)
	}

	return result.DeletedCount, nil
}

// SearchChats does a server-side full-text search across the user's chats.
//
// Per-user AES encryption (intentional privacy guarantee) rules out a
// MySQL/Mongo FULLTEXT index — the ciphertext is opaque. So we list the
// user's chats, decrypt each one's messages, scan in memory. Cost is O(n)
// per query but n is per-user (typically tens to hundreds of conversations);
// each chat's messages live in a single decryption call, so we trade
// search-time CPU for not-needing a separate blind-index store.
//
// Returns up to `limit` hits, sorted most-recent first. Each hit includes
// a short snippet around the match so the UI can preview without a roundtrip.
func (s *ChatSyncService) SearchChats(ctx context.Context, userID, query string, limit int) (*models.ChatSearchResponse, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID is required")
	}
	query = strings.TrimSpace(query)
	if len(query) < 2 {
		return &models.ChatSearchResponse{Query: query, Hits: []models.ChatSearchHit{}}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	needle := strings.ToLower(query)

	// Scan most recent chats first (matches user expectation: "I asked it
	// recently"). Cap the per-user scan at 500 chats — beyond that we'd
	// want a real index. Logs a warning if we hit the cap so we know to
	// invest in one.
	const maxScan = 500
	opts := options.Find().
		SetSort(bson.D{{Key: "updatedAt", Value: -1}}).
		SetLimit(maxScan).
		SetProjection(bson.M{
			"chatId":            1,
			"title":             1,
			"isStarred":         1,
			"updatedAt":         1,
			"encryptedMessages": 1,
		})

	cursor, err := s.collection.Find(ctx, bson.M{"userId": userID}, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to query chats: %w", err)
	}
	defer cursor.Close(ctx)

	hits := make([]models.ChatSearchHit, 0, limit)
	scanned := 0
	for cursor.Next(ctx) {
		if len(hits) >= limit {
			break
		}
		scanned++
		var encChat models.EncryptedChat
		if err := cursor.Decode(&encChat); err != nil {
			continue
		}

		// Cheap title check first — avoids decrypting if the title alone
		// matches and we have enough hits already.
		titleMatch := strings.Contains(strings.ToLower(encChat.Title), needle)

		messages, err := s.decryptMessages(userID, encChat.EncryptedMessages)
		if err != nil {
			if titleMatch {
				hits = append(hits, models.ChatSearchHit{
					ChatID:    encChat.ChatID,
					Title:     encChat.Title,
					IsStarred: encChat.IsStarred,
					UpdatedAt: encChat.UpdatedAt,
					Snippet:   encChat.Title,
					MatchField: "title",
				})
			}
			continue
		}

		// Pick the most recent matching message — most useful snippet.
		var match *models.ChatSearchHit
		for i := len(messages) - 1; i >= 0; i-- {
			content := messages[i].Content
			lc := strings.ToLower(content)
			idx := strings.Index(lc, needle)
			if idx < 0 {
				continue
			}
			match = &models.ChatSearchHit{
				ChatID:        encChat.ChatID,
				Title:         encChat.Title,
				IsStarred:     encChat.IsStarred,
				UpdatedAt:     encChat.UpdatedAt,
				Snippet:       snippetAround(content, idx, len(needle), 80),
				MatchField:    "message",
				MessageID:     messages[i].ID,
				MessageRole:   messages[i].Role,
				MessageIndex:  i,
			}
			break
		}
		if match != nil {
			hits = append(hits, *match)
		} else if titleMatch {
			hits = append(hits, models.ChatSearchHit{
				ChatID:     encChat.ChatID,
				Title:      encChat.Title,
				IsStarred:  encChat.IsStarred,
				UpdatedAt:  encChat.UpdatedAt,
				Snippet:    encChat.Title,
				MatchField: "title",
			})
		}
	}

	if scanned >= maxScan {
		log.Printf("⚠️ [SEARCH] Hit maxScan=%d for user %s query=%q — consider an index", maxScan, userID, query)
	}

	return &models.ChatSearchResponse{
		Query: query,
		Hits:  hits,
		Total: len(hits),
	}, nil
}

// snippetAround returns ~window chars on each side of an index into s,
// trimmed to word-ish boundaries. Used to give the UI a quick preview.
func snippetAround(s string, matchIdx, matchLen, window int) string {
	start := matchIdx - window
	if start < 0 {
		start = 0
	}
	end := matchIdx + matchLen + window
	if end > len(s) {
		end = len(s)
	}
	out := s[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(s) {
		out = out + "…"
	}
	// Collapse internal whitespace to single spaces so the chip stays compact.
	return strings.Join(strings.Fields(out), " ")
}

// decryptMessages decrypts and decompresses the encrypted messages JSON
func (s *ChatSyncService) decryptMessages(userID, encryptedMessages string) ([]models.ChatMessage, error) {
	if encryptedMessages == "" {
		return []models.ChatMessage{}, nil
	}

	// Decompress if compressed (backward compatible - old data won't have gzip: prefix)
	dataToDecrypt := encryptedMessages
	if strings.HasPrefix(encryptedMessages, "gzip:") {
		compressed := strings.TrimPrefix(encryptedMessages, "gzip:")
		decompressed, err := s.decompressData(compressed)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress messages: %w", err)
		}
		dataToDecrypt = decompressed
	}

	decrypted, err := s.encryptionService.Decrypt(userID, dataToDecrypt)
	if err != nil {
		return nil, err
	}

	var messages []models.ChatMessage
	if err := json.Unmarshal(decrypted, &messages); err != nil {
		return nil, fmt.Errorf("failed to parse messages: %w", err)
	}

	return messages, nil
}

// compressData compresses a string using gzip and returns it with a prefix marker
func (s *ChatSyncService) compressData(data string) (string, error) {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)

	if _, err := writer.Write([]byte(data)); err != nil {
		return "", err
	}

	if err := writer.Close(); err != nil {
		return "", err
	}

	// Encode to base64 and add prefix to identify compressed data
	compressed := base64.StdEncoding.EncodeToString(buf.Bytes())
	return "gzip:" + compressed, nil
}

// decompressData decompresses a base64-encoded gzip string
func (s *ChatSyncService) decompressData(compressed string) (string, error) {
	// Decode base64
	data, err := base64.StdEncoding.DecodeString(compressed)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}

	// Decompress gzip
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read decompressed data: %w", err)
	}

	return string(decompressed), nil
}

// EnsureIndexes creates necessary indexes for the chats collection
func (s *ChatSyncService) EnsureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "updatedAt", Value: -1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "chatId", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "isStarred", Value: 1}}},
	}

	_, err := s.collection.Indexes().CreateMany(ctx, indexes)
	if err != nil {
		return fmt.Errorf("failed to create chat indexes: %w", err)
	}

	return nil
}
