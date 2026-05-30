package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"claraverse/internal/crypto"
	"claraverse/internal/database"
	"claraverse/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MemoryStorageService handles CRUD operations for memories with encryption and deduplication
type MemoryStorageService struct {
	mongodb           *database.MongoDB
	collection        *mongo.Collection
	encryptionService *crypto.EncryptionService
	embeddingService  *EmbeddingService // optional; nil means embeddings disabled
}

// NewMemoryStorageService creates a new memory storage service
func NewMemoryStorageService(mongodb *database.MongoDB, encryptionService *crypto.EncryptionService) *MemoryStorageService {
	return &MemoryStorageService{
		mongodb:           mongodb,
		collection:        mongodb.Collection(database.CollectionMemories),
		encryptionService: encryptionService,
	}
}

// SetEmbeddingService wires the embedding service after both have been
// constructed (avoids an init-order cycle in main.go). When unset, the
// storage service skips embedding generation and SearchByEmbedding becomes
// a no-op — the selection service falls back to its LLM-only path.
func (s *MemoryStorageService) SetEmbeddingService(emb *EmbeddingService) {
	s.embeddingService = emb
	if emb != nil {
		log.Printf("📐 [MEMORY-STORAGE] Embedding service attached — new memories will be vectorized")
	}
}

// CreateMemory creates a new memory with encryption and deduplication
func (s *MemoryStorageService) CreateMemory(ctx context.Context, userID, content, category string, tags []string, sourceEngagement float64, conversationID string) (*models.Memory, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID is required")
	}
	if content == "" {
		return nil, fmt.Errorf("memory content is required")
	}

	// Normalize and hash content for deduplication
	normalizedContent := s.normalizeContent(content)
	contentHash := s.calculateHash(normalizedContent)

	// Check for duplicate
	existingMemory, err := s.CheckDuplicate(ctx, userID, contentHash)
	if err != nil && err != mongo.ErrNoDocuments {
		return nil, fmt.Errorf("failed to check duplicate: %w", err)
	}

	// If duplicate exists, update it instead
	if existingMemory != nil {
		log.Printf("🔄 [MEMORY-STORAGE] Duplicate memory found (ID: %s), updating instead", existingMemory.ID.Hex())
		return s.UpdateExistingMemory(ctx, existingMemory, tags, sourceEngagement)
	}

	// Encrypt content
	encryptedContent, err := s.encryptionService.Encrypt(userID, []byte(content))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt memory content: %w", err)
	}

	// Initial score is based solely on source engagement
	initialScore := sourceEngagement

	// Pre-compute the embedding if a provider is configured. We do this
	// before insert so the memory lands ready for vector retrieval on the
	// very next turn. If the embed call fails, log and proceed without —
	// LLM-based selection still works.
	var embedding []float32
	if s.embeddingService != nil {
		if vec, embErr := s.embeddingService.Embed(ctx, content); embErr == nil {
			embedding = vec
		} else {
			log.Printf("⚠️ [MEMORY-STORAGE] Embedding failed for new memory (skipping): %v", embErr)
		}
	}

	// Create new memory
	memory := &models.Memory{
		ID:               primitive.NewObjectID(),
		UserID:           userID,
		ConversationID:   conversationID,
		EncryptedContent: encryptedContent,
		ContentHash:      contentHash,
		Category:         category,
		Tags:             tags,
		Score:            initialScore,
		AccessCount:      0,
		LastAccessedAt:   nil,
		IsArchived:       false,
		ArchivedAt:       nil,
		SourceEngagement: sourceEngagement,
		Embedding:        embedding,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		Version:          1,
	}

	// Insert into database
	_, err = s.collection.InsertOne(ctx, memory)
	if err != nil {
		return nil, fmt.Errorf("failed to insert memory: %w", err)
	}

	log.Printf("✅ [MEMORY-STORAGE] Created new memory (ID: %s, Category: %s, Score: %.2f, embedded: %v)", memory.ID.Hex(), category, initialScore, len(embedding) > 0)
	return memory, nil
}

// UpdateExistingMemory updates an existing memory (for deduplication)
func (s *MemoryStorageService) UpdateExistingMemory(ctx context.Context, memory *models.Memory, newTags []string, sourceEngagement float64) (*models.Memory, error) {
	// Merge tags (avoid duplicates)
	tagMap := make(map[string]bool)
	for _, tag := range memory.Tags {
		tagMap[tag] = true
	}
	for _, tag := range newTags {
		tagMap[tag] = true
	}
	mergedTags := make([]string, 0, len(tagMap))
	for tag := range tagMap {
		mergedTags = append(mergedTags, tag)
	}

	// Boost score slightly on re-mention (indicates importance)
	newScore := memory.Score + 0.1
	if newScore > 1.0 {
		newScore = 1.0
	}

	// Update engagement if higher
	if sourceEngagement > memory.SourceEngagement {
		memory.SourceEngagement = sourceEngagement
	}

	// Update memory
	update := bson.M{
		"$set": bson.M{
			"tags":             mergedTags,
			"score":            newScore,
			"sourceEngagement": memory.SourceEngagement,
			"updatedAt":        time.Now(),
		},
		"$inc": bson.M{
			"version": 1,
		},
	}

	result := s.collection.FindOneAndUpdate(
		ctx,
		bson.M{"_id": memory.ID},
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)

	var updatedMemory models.Memory
	if err := result.Decode(&updatedMemory); err != nil {
		return nil, fmt.Errorf("failed to decode updated memory: %w", err)
	}

	log.Printf("🔄 [MEMORY-STORAGE] Updated memory (ID: %s, New Score: %.2f, Version: %d)", updatedMemory.ID.Hex(), newScore, updatedMemory.Version)
	return &updatedMemory, nil
}

// UpdateMemoryInPlace atomically updates a memory (content, category, tags)
// SECURITY: Replaces delete-create pattern to prevent race conditions
func (s *MemoryStorageService) UpdateMemoryInPlace(
	ctx context.Context,
	userID string,
	memoryID primitive.ObjectID,
	content string,
	category string,
	tags []string,
	sourceEngagement float64,
	conversationID string,
) (*models.Memory, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID is required")
	}
	if content == "" {
		return nil, fmt.Errorf("memory content is required")
	}

	// Normalize and hash new content
	normalizedContent := s.normalizeContent(content)
	contentHash := s.calculateHash(normalizedContent)

	// Encrypt new content
	encryptedContent, err := s.encryptionService.Encrypt(userID, []byte(content))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt memory content: %w", err)
	}

	now := time.Now()

	// Atomic update with user authorization check
	update := bson.M{
		"$set": bson.M{
			"encryptedContent": encryptedContent,
			"contentHash":      contentHash,
			"category":         category,
			"tags":             tags,
			"sourceEngagement": sourceEngagement,
			"conversationId":   conversationID,
			"updatedAt":        now,
		},
		"$inc": bson.M{
			"version": 1,
		},
	}

	// SECURITY: Filter includes userId to prevent unauthorized updates
	filter := bson.M{
		"_id":    memoryID,
		"userId": userID, // Critical: ensures user can only update their own memories
	}

	result := s.collection.FindOneAndUpdate(
		ctx,
		filter,
		update,
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)

	var updatedMemory models.Memory
	if err := result.Decode(&updatedMemory); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("memory not found or access denied")
		}
		return nil, fmt.Errorf("failed to update memory: %w", err)
	}

	log.Printf("✅ [MEMORY-STORAGE] Updated memory atomically (ID: %s, Version: %d)", updatedMemory.ID.Hex(), updatedMemory.Version)
	return &updatedMemory, nil
}

// GetMemory retrieves and decrypts a single memory
func (s *MemoryStorageService) GetMemory(ctx context.Context, userID string, memoryID primitive.ObjectID) (*models.DecryptedMemory, error) {
	var memory models.Memory
	err := s.collection.FindOne(ctx, bson.M{"_id": memoryID, "userId": userID}).Decode(&memory)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("memory not found")
		}
		return nil, fmt.Errorf("failed to get memory: %w", err)
	}

	// Decrypt content
	decryptedBytes, err := s.encryptionService.Decrypt(userID, memory.EncryptedContent)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt memory: %w", err)
	}

	decryptedMemory := &models.DecryptedMemory{
		Memory:           memory,
		DecryptedContent: string(decryptedBytes),
	}

	return decryptedMemory, nil
}

// ListMemories retrieves memories with optional filters and pagination
func (s *MemoryStorageService) ListMemories(ctx context.Context, userID string, category string, tags []string, includeArchived bool, page, pageSize int) ([]models.DecryptedMemory, int64, error) {
	// Build filter
	filter := bson.M{"userId": userID}

	if !includeArchived {
		filter["isArchived"] = false
	}

	if category != "" {
		filter["category"] = category
	}

	if len(tags) > 0 {
		filter["tags"] = bson.M{"$in": tags}
	}

	// Count total
	total, err := s.collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count memories: %w", err)
	}

	// Calculate pagination
	skip := (page - 1) * pageSize
	findOptions := options.Find().
		SetSort(bson.D{{Key: "score", Value: -1}, {Key: "updatedAt", Value: -1}}).
		SetSkip(int64(skip)).
		SetLimit(int64(pageSize))

	// Find memories
	cursor, err := s.collection.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to find memories: %w", err)
	}
	defer cursor.Close(ctx)

	var memories []models.Memory
	if err := cursor.All(ctx, &memories); err != nil {
		return nil, 0, fmt.Errorf("failed to decode memories: %w", err)
	}

	// Decrypt all memories
	decryptedMemories := make([]models.DecryptedMemory, 0, len(memories))
	for _, memory := range memories {
		decryptedBytes, err := s.encryptionService.Decrypt(userID, memory.EncryptedContent)
		if err != nil {
			log.Printf("⚠️ [MEMORY-STORAGE] Failed to decrypt memory %s: %v", memory.ID.Hex(), err)
			continue
		}

		decryptedMemories = append(decryptedMemories, models.DecryptedMemory{
			Memory:           memory,
			DecryptedContent: string(decryptedBytes),
		})
	}

	return decryptedMemories, total, nil
}

// GetActiveMemories retrieves all active (non-archived) memories for a user (decrypted)
func (s *MemoryStorageService) GetActiveMemories(ctx context.Context, userID string) ([]models.DecryptedMemory, error) {
	filter := bson.M{
		"userId":     userID,
		"isArchived": false,
	}

	findOptions := options.Find().SetSort(bson.D{{Key: "score", Value: -1}})

	cursor, err := s.collection.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to find active memories: %w", err)
	}
	defer cursor.Close(ctx)

	var memories []models.Memory
	if err := cursor.All(ctx, &memories); err != nil {
		return nil, fmt.Errorf("failed to decode memories: %w", err)
	}

	// Decrypt all memories
	decryptedMemories := make([]models.DecryptedMemory, 0, len(memories))
	for _, memory := range memories {
		decryptedBytes, err := s.encryptionService.Decrypt(userID, memory.EncryptedContent)
		if err != nil {
			log.Printf("⚠️ [MEMORY-STORAGE] Failed to decrypt memory %s: %v", memory.ID.Hex(), err)
			continue
		}

		decryptedMemories = append(decryptedMemories, models.DecryptedMemory{
			Memory:           memory,
			DecryptedContent: string(decryptedBytes),
		})
	}

	log.Printf("📚 [MEMORY-STORAGE] Retrieved %d active memories for user %s", len(decryptedMemories), userID)
	return decryptedMemories, nil
}

// MemoryHit is a single result from SearchByEmbedding: the memory + its
// cosine similarity to the query vector. Selection callers use Score to
// optionally rerank or filter.
type MemoryHit struct {
	Memory     models.DecryptedMemory
	Similarity float64
}

// SearchByEmbedding finds the top-K memories most similar to queryVec using
// cosine similarity over the per-user active memory set. We load everything
// into memory and score in a single pass — fine while users stay under
// ~thousands of memories. For higher scale, swap this for Mongo Atlas
// $vectorSearch or a dedicated ANN store.
//
// Legacy memories without an embedding (created before this feature) are
// included in the candidate pool with a similarity of 0 — they only surface
// if topK > #embedded memories, which keeps backward-compat without
// blocking on a backfill.
func (s *MemoryStorageService) SearchByEmbedding(ctx context.Context, userID string, queryVec []float32, topK int) ([]MemoryHit, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID is required")
	}
	if len(queryVec) == 0 {
		return nil, fmt.Errorf("empty query vector")
	}
	if topK <= 0 || topK > 200 {
		topK = 10
	}

	// Use the existing accessor; it already decrypts.
	candidates, err := s.GetActiveMemories(ctx, userID)
	if err != nil {
		return nil, err
	}

	hits := make([]MemoryHit, 0, len(candidates))
	for _, m := range candidates {
		sim := 0.0
		if len(m.Embedding) > 0 {
			sim = CosineSimilarity(queryVec, m.Embedding)
		}
		hits = append(hits, MemoryHit{Memory: m, Similarity: sim})
	}

	// Stable sort would be nicer; insertion sort over a tiny list with a
	// single pass is more than fast enough — but for clarity, just do a
	// straightforward sort with the stdlib.
	sortHitsDesc(hits)

	if len(hits) > topK {
		hits = hits[:topK]
	}
	log.Printf("🔎 [MEMORY-STORAGE] Vector search returned top %d (of %d candidates) for user %s", len(hits), len(candidates), userID)
	return hits, nil
}

// sortHitsDesc orders by Similarity descending. Pulled out so the test
// suite can use it if it ever wants to verify ordering deterministically.
func sortHitsDesc(hits []MemoryHit) {
	// Simple insertion sort — pool stays small (hundreds tops).
	for i := 1; i < len(hits); i++ {
		j := i
		for j > 0 && hits[j-1].Similarity < hits[j].Similarity {
			hits[j-1], hits[j] = hits[j], hits[j-1]
			j--
		}
	}
}

// UpdateMemoryAccess increments access count and updates last accessed timestamp
func (s *MemoryStorageService) UpdateMemoryAccess(ctx context.Context, memoryIDs []primitive.ObjectID) error {
	if len(memoryIDs) == 0 {
		return nil
	}

	now := time.Now()
	filter := bson.M{"_id": bson.M{"$in": memoryIDs}}
	update := bson.M{
		"$inc": bson.M{"accessCount": 1},
		"$set": bson.M{"lastAccessedAt": now},
	}

	result, err := s.collection.UpdateMany(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("failed to update memory access: %w", err)
	}

	log.Printf("📊 [MEMORY-STORAGE] Updated access for %d memories", result.ModifiedCount)
	return nil
}

// ArchiveMemory marks a memory as archived
func (s *MemoryStorageService) ArchiveMemory(ctx context.Context, userID string, memoryID primitive.ObjectID) error {
	now := time.Now()
	update := bson.M{
		"$set": bson.M{
			"isArchived": true,
			"archivedAt": now,
			"updatedAt":  now,
		},
	}

	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": memoryID, "userId": userID}, update)
	if err != nil {
		return fmt.Errorf("failed to archive memory: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("memory not found or access denied")
	}

	log.Printf("📦 [MEMORY-STORAGE] Archived memory %s", memoryID.Hex())
	return nil
}

// UnarchiveMemory restores an archived memory
func (s *MemoryStorageService) UnarchiveMemory(ctx context.Context, userID string, memoryID primitive.ObjectID) error {
	update := bson.M{
		"$set": bson.M{
			"isArchived": false,
			"archivedAt": nil,
			"updatedAt":  time.Now(),
		},
	}

	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": memoryID, "userId": userID}, update)
	if err != nil {
		return fmt.Errorf("failed to unarchive memory: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("memory not found or access denied")
	}

	log.Printf("📤 [MEMORY-STORAGE] Unarchived memory %s", memoryID.Hex())
	return nil
}

// DeleteMemory permanently deletes a memory
func (s *MemoryStorageService) DeleteMemory(ctx context.Context, userID string, memoryID primitive.ObjectID) error {
	result, err := s.collection.DeleteOne(ctx, bson.M{"_id": memoryID, "userId": userID})
	if err != nil {
		return fmt.Errorf("failed to delete memory: %w", err)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("memory not found or access denied")
	}

	log.Printf("🗑️ [MEMORY-STORAGE] Deleted memory %s", memoryID.Hex())
	return nil
}

// CheckDuplicate checks if a memory with the same content hash exists
func (s *MemoryStorageService) CheckDuplicate(ctx context.Context, userID, contentHash string) (*models.Memory, error) {
	var memory models.Memory
	err := s.collection.FindOne(ctx, bson.M{"userId": userID, "contentHash": contentHash}).Decode(&memory)
	if err != nil {
		return nil, err
	}
	return &memory, nil
}

// normalizeContent normalizes content for deduplication
func (s *MemoryStorageService) normalizeContent(content string) string {
	// Convert to lowercase
	normalized := strings.ToLower(content)

	// Replace word separators with spaces first (before removing other punctuation)
	// This prevents words from merging when punctuation is removed
	normalized = strings.ReplaceAll(normalized, "\n", " ")
	normalized = strings.ReplaceAll(normalized, "\t", " ")
	normalized = strings.ReplaceAll(normalized, "\r", " ")
	normalized = strings.ReplaceAll(normalized, "-", " ")
	normalized = strings.ReplaceAll(normalized, "_", " ")

	// Trim whitespace
	normalized = strings.TrimSpace(normalized)

	// Remove punctuation (simple version)
	normalized = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			return r
		}
		return -1
	}, normalized)

	// Collapse multiple spaces
	normalized = strings.Join(strings.Fields(normalized), " ")
	return normalized
}

// calculateHash calculates SHA-256 hash of content
func (s *MemoryStorageService) calculateHash(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// GetMemoryStats returns statistics about user's memories
func (s *MemoryStorageService) GetMemoryStats(ctx context.Context, userID string) (map[string]interface{}, error) {
	// Count total memories
	total, err := s.collection.CountDocuments(ctx, bson.M{"userId": userID})
	if err != nil {
		return nil, fmt.Errorf("failed to count total memories: %w", err)
	}

	// Count active memories
	active, err := s.collection.CountDocuments(ctx, bson.M{"userId": userID, "isArchived": false})
	if err != nil {
		return nil, fmt.Errorf("failed to count active memories: %w", err)
	}

	// Count archived memories
	archived, err := s.collection.CountDocuments(ctx, bson.M{"userId": userID, "isArchived": true})
	if err != nil {
		return nil, fmt.Errorf("failed to count archived memories: %w", err)
	}

	// Calculate average score
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"userId": userID, "isArchived": false}}},
		{{Key: "$group", Value: bson.M{
			"_id":      nil,
			"avgScore": bson.M{"$avg": "$score"},
		}}},
	}

	cursor, err := s.collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate scores: %w", err)
	}
	defer cursor.Close(ctx)

	var avgScoreResult struct {
		AvgScore float64 `bson:"avgScore"`
	}
	avgScore := 0.0
	if cursor.Next(ctx) {
		if err := cursor.Decode(&avgScoreResult); err == nil {
			avgScore = avgScoreResult.AvgScore
		}
	}

	stats := map[string]interface{}{
		"total_memories":    total,
		"active_memories":   active,
		"archived_memories": archived,
		"avg_score":         avgScore,
	}

	return stats, nil
}
