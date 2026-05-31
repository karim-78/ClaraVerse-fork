package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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

// MemoryExtractionService handles extraction of memories from conversations using LLMs
type MemoryExtractionService struct {
	mongodb              *database.MongoDB
	jobCollection        *mongo.Collection
	engagementCollection *mongo.Collection
	encryptionService    *crypto.EncryptionService
	providerService      *ProviderService
	memoryStorageService *MemoryStorageService
	chatService          *ChatService
	settingsService      *SettingsService
	modelPool            *MemoryModelPool // Dynamic model pool with round-robin and failover
}

// Rate limiting constants for extraction to prevent abuse
const (
	MaxExtractionsPerHour = 20  // Maximum extractions per user per hour
	MaxPendingJobsPerUser = 50  // Maximum pending jobs per user
)

// Memory extraction system prompt
const MemoryExtractionSystemPrompt = `You are a memory extraction system for ClaraVerse. Analyze this conversation and extract important information to remember about the user.

WHAT TO EXTRACT:
1. **Personal Information**: Name, location, occupation, family, age, background
2. **Preferences**: Likes, dislikes, communication style, how they want to be addressed
3. **Important Context**: Ongoing projects, goals, constraints, responsibilities
4. **Facts**: Skills, experiences, knowledge areas, technical expertise
5. **Instructions**: Specific guidelines the user wants you to follow (e.g., "always use TypeScript", "keep responses brief")

RULES:
- Be concise (1-2 sentences per memory)
- Only extract FACTUAL information explicitly stated by the user
- Ignore small talk and pleasantries
- Avoid redundant or obvious information
- Each memory should be atomic (one piece of information)
- Categorize each memory correctly
- Add relevant tags for searchability
- **CRITICAL**: DO NOT extract information that is already captured in EXISTING MEMORIES (provided below)
- Only extract NEW information not present in existing memories
- If conversation contains no new memorable information, return empty array

CATEGORIES:
- "personal_info": Name, location, occupation, family, age
- "preferences": Likes, dislikes, style, communication preferences
- "context": Ongoing projects, goals, responsibilities
- "fact": Skills, knowledge, experiences
- "instruction": Guidelines to follow

Return JSON with array of memories.`

// memoryExtractionSchema defines structured output for memory extraction
var memoryExtractionSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"memories": map[string]interface{}{
			"type": "array",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content": map[string]interface{}{
						"type":        "string",
						"description": "The memory content (concise, factual)",
					},
					"category": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"personal_info", "preferences", "context", "fact", "instruction"},
						"description": "Memory category",
					},
					"tags": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Relevant tags for this memory",
					},
				},
				"required":             []string{"content", "category", "tags"},
				"additionalProperties": false,
			},
		},
	},
	"required":             []string{"memories"},
	"additionalProperties": false,
}

// NewMemoryExtractionService creates a new memory extraction service
func NewMemoryExtractionService(
	mongodb *database.MongoDB,
	encryptionService *crypto.EncryptionService,
	providerService *ProviderService,
	memoryStorageService *MemoryStorageService,
	chatService *ChatService,
	modelPool *MemoryModelPool,
) *MemoryExtractionService {
	return &MemoryExtractionService{
		mongodb:              mongodb,
		jobCollection:        mongodb.Collection(database.CollectionMemoryExtractionJobs),
		engagementCollection: mongodb.Collection(database.CollectionConversationEngagement),
		encryptionService:    encryptionService,
		providerService:      providerService,
		memoryStorageService: memoryStorageService,
		chatService:          chatService,
		modelPool:            modelPool,
	}
}

// SetSettingsService sets the settings service for system-wide model assignments
func (s *MemoryExtractionService) SetSettingsService(settingsService *SettingsService) {
	s.settingsService = settingsService
	log.Printf("✅ [MEMORY-EXTRACTION] Settings service set for system model assignment")
}

// StorageService exposes the underlying memory storage service so the
// chat-side memory tools (add_memory / search_memory) can read+write
// without us adding a separate dependency on chat_service.
func (s *MemoryExtractionService) StorageService() *MemoryStorageService {
	return s.memoryStorageService
}

// EnqueueExtraction creates a new extraction job (non-blocking)
// SECURITY: Includes rate limiting to prevent abuse and DoS attacks
func (s *MemoryExtractionService) EnqueueExtraction(
	ctx context.Context,
	userID string,
	conversationID string,
	messages []map[string]interface{},
) error {
	if userID == "" || conversationID == "" {
		return fmt.Errorf("user ID and conversation ID are required")
	}

	// SECURITY: Check pending jobs limit to prevent queue flooding
	pendingCount, err := s.jobCollection.CountDocuments(ctx, bson.M{
		"userId": userID,
		"status": models.JobStatusPending,
	})
	if err != nil {
		log.Printf("⚠️ [MEMORY-EXTRACTION] Failed to count pending jobs: %v", err)
	} else if pendingCount >= MaxPendingJobsPerUser {
		log.Printf("⚠️ [MEMORY-EXTRACTION] User %s has %d pending jobs (max: %d), skipping", userID, pendingCount, MaxPendingJobsPerUser)
		return fmt.Errorf("too many pending extraction jobs (%d), please wait", pendingCount)
	}

	// SECURITY: Check hourly extraction rate (last hour completed + pending)
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	recentCount, err := s.jobCollection.CountDocuments(ctx, bson.M{
		"userId": userID,
		"$or": []bson.M{
			{"status": models.JobStatusPending},
			{"status": models.JobStatusProcessing},
			{
				"status":      models.JobStatusCompleted,
				"processedAt": bson.M{"$gte": oneHourAgo},
			},
		},
	})
	if err != nil {
		log.Printf("⚠️ [MEMORY-EXTRACTION] Failed to count recent jobs: %v", err)
	} else if recentCount >= MaxExtractionsPerHour {
		log.Printf("⚠️ [MEMORY-EXTRACTION] User %s exceeded hourly extraction limit (%d/%d)", userID, recentCount, MaxExtractionsPerHour)
		return fmt.Errorf("extraction rate limit exceeded (%d extractions in last hour), please wait", recentCount)
	}

	// Encrypt messages
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("failed to marshal messages: %w", err)
	}

	encryptedMessages, err := s.encryptionService.Encrypt(userID, messagesJSON)
	if err != nil {
		return fmt.Errorf("failed to encrypt messages: %w", err)
	}

	// Create job
	job := &models.MemoryExtractionJob{
		ID:                primitive.NewObjectID(),
		UserID:            userID,
		ConversationID:    conversationID,
		MessageCount:      len(messages),
		EncryptedMessages: encryptedMessages,
		Status:            models.JobStatusPending,
		AttemptCount:      0,
		CreatedAt:         time.Now(),
	}

	// Insert job
	_, err = s.jobCollection.InsertOne(ctx, job)
	if err != nil {
		return fmt.Errorf("failed to insert extraction job: %w", err)
	}

	log.Printf("📥 [MEMORY-EXTRACTION] Enqueued job for conversation %s (%d messages)", conversationID, len(messages))
	return nil
}

// ProcessPendingJobs processes all pending extraction jobs (background worker)
func (s *MemoryExtractionService) ProcessPendingJobs(ctx context.Context) error {
	// Find pending jobs
	filter := bson.M{"status": models.JobStatusPending}
	cursor, err := s.jobCollection.Find(ctx, filter)
	if err != nil {
		return fmt.Errorf("failed to find pending jobs: %w", err)
	}
	defer cursor.Close(ctx)

	var jobs []models.MemoryExtractionJob
	if err := cursor.All(ctx, &jobs); err != nil {
		return fmt.Errorf("failed to decode jobs: %w", err)
	}

	if len(jobs) == 0 {
		return nil // No pending jobs
	}

	log.Printf("⚙️ [MEMORY-EXTRACTION] Processing %d pending jobs", len(jobs))

	// Process each job
	for _, job := range jobs {
		if err := s.processJob(ctx, &job); err != nil {
			log.Printf("⚠️ [MEMORY-EXTRACTION] Job %s failed: %v", job.ID.Hex(), err)
			s.markJobFailed(ctx, job.ID, err.Error())
		}
	}

	return nil
}

// processJob processes a single extraction job
func (s *MemoryExtractionService) processJob(ctx context.Context, job *models.MemoryExtractionJob) error {
	// Mark as processing
	s.updateJobStatus(ctx, job.ID, models.JobStatusProcessing)

	// Decrypt messages
	messagesBytes, err := s.encryptionService.Decrypt(job.UserID, job.EncryptedMessages)
	if err != nil {
		return fmt.Errorf("failed to decrypt messages: %w", err)
	}

	var messages []map[string]interface{}
	if err := json.Unmarshal(messagesBytes, &messages); err != nil {
		return fmt.Errorf("failed to unmarshal messages: %w", err)
	}

	log.Printf("🔍 [MEMORY-EXTRACTION] Processing job %s (%d messages)", job.ID.Hex(), len(messages))

	// Calculate conversation engagement
	engagement := s.calculateEngagement(messages)

	// Store engagement in database
	if err := s.storeEngagement(ctx, job.UserID, job.ConversationID, messages, engagement); err != nil {
		log.Printf("⚠️ [MEMORY-EXTRACTION] Failed to store engagement: %v", err)
	}

	// Fetch existing memories to avoid duplicates
	existingMemories, _, err := s.memoryStorageService.ListMemories(
		ctx,
		job.UserID,
		"",    // category (empty = all categories)
		nil,   // tags (nil = all tags)
		false, // includeArchived (false = only active)
		1,     // page
		100,   // pageSize (get recent 100 memories for context)
	)
	if err != nil {
		log.Printf("⚠️ [MEMORY-EXTRACTION] Failed to fetch existing memories: %v, continuing without context", err)
		existingMemories = []models.DecryptedMemory{} // Continue with empty list
	}

	log.Printf("📚 [MEMORY-EXTRACTION] Found %d existing memories to avoid duplicates", len(existingMemories))

	// Extract memories via LLM (with existing memories for context)
	extractedMemories, err := s.extractMemories(ctx, job.UserID, messages, existingMemories)
	if err != nil {
		return fmt.Errorf("failed to extract memories: %w", err)
	}

	log.Printf("🧠 [MEMORY-EXTRACTION] Extracted %d memories", len(extractedMemories.Memories))

	// Store each memory
	for _, mem := range extractedMemories.Memories {
		_, err := s.memoryStorageService.CreateMemory(
			ctx,
			job.UserID,
			mem.Content,
			mem.Category,
			mem.Tags,
			engagement,
			job.ConversationID,
		)
		if err != nil {
			log.Printf("⚠️ [MEMORY-EXTRACTION] Failed to store memory: %v", err)
		}
	}

	// Mark job as completed
	s.markJobCompleted(ctx, job.ID)

	log.Printf("✅ [MEMORY-EXTRACTION] Job %s completed successfully", job.ID.Hex())
	return nil
}

// extractMemories calls LLM to extract memories from conversation with automatic failover
func (s *MemoryExtractionService) extractMemories(
	ctx context.Context,
	userID string,
	messages []map[string]interface{},
	existingMemories []models.DecryptedMemory,
) (*models.ExtractedMemoryFromLLM, error) {

	// Check if user has a custom extractor model preference
	userPreferredModel, err := s.getExtractorModelForUser(ctx, userID)
	var extractorModelID string

	if err == nil && userPreferredModel != "" {
		// User has a preference, use it
		extractorModelID = userPreferredModel
		log.Printf("👤 [MEMORY-EXTRACTION] Using user-preferred model: %s", extractorModelID)
	} else {
		// No user preference, get from model pool
		extractorModelID, err = s.modelPool.GetNextExtractor()
		if err != nil {
			return nil, fmt.Errorf("no extractor models available: %w", err)
		}
	}

	// Try extraction with automatic failover (max 3 attempts)
	maxAttempts := 3
	var lastError error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := s.tryExtraction(ctx, userID, extractorModelID, messages, existingMemories)

		if err == nil {
			// Success!
			s.modelPool.MarkSuccess(extractorModelID)
			return result, nil
		}

		// Extraction failed
		lastError = err
		s.modelPool.MarkFailure(extractorModelID)
		log.Printf("⚠️ [MEMORY-EXTRACTION] Attempt %d/%d failed with model %s: %v",
			attempt, maxAttempts, extractorModelID, err)

		// If not last attempt, get next model from pool
		if attempt < maxAttempts {
			extractorModelID, err = s.modelPool.GetNextExtractor()
			if err != nil {
				return nil, fmt.Errorf("no more extractors available after %d attempts: %w", attempt, err)
			}
			log.Printf("🔄 [MEMORY-EXTRACTION] Retrying with next model: %s", extractorModelID)
		}
	}

	return nil, fmt.Errorf("extraction failed after %d attempts, last error: %w", maxAttempts, lastError)
}

// tryExtraction attempts extraction with a specific model (internal helper)
func (s *MemoryExtractionService) tryExtraction(
	ctx context.Context,
	userID string,
	extractorModelID string,
	messages []map[string]interface{},
	existingMemories []models.DecryptedMemory,
) (*models.ExtractedMemoryFromLLM, error) {

	// Get provider and model
	provider, actualModel, err := s.getProviderAndModel(extractorModelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider for extractor: %w", err)
	}

	log.Printf("🤖 [MEMORY-EXTRACTION] Using model: %s (%s)", extractorModelID, actualModel)

	// Build conversation transcript
	conversationTranscript := s.buildConversationTranscript(messages)

	// Build existing memories context (decrypted)
	existingMemoriesContext := s.buildExistingMemoriesContext(ctx, userID, existingMemories)

	// Build user prompt with existing memories
	userPrompt := fmt.Sprintf(`EXISTING MEMORIES:
%s

CONVERSATION:
%s

Analyze this conversation and extract ONLY NEW memories that are NOT already captured in the existing memories above. Return JSON with array of memories. If no new information, return empty array.`, existingMemoriesContext, conversationTranscript)

	// Build messages
	llmMessages := []map[string]interface{}{
		{
			"role":    "system",
			"content": MemoryExtractionSystemPrompt,
		},
		{
			"role":    "user",
			"content": userPrompt,
		},
	}

	// Build request with structured output
	requestBody := map[string]interface{}{
		"model":       actualModel,
		"messages":    llmMessages,
		"stream":      false,
		"temperature": 0.3, // Low temp for consistency
		"response_format": map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "memory_extraction",
				"strict": true,
				"schema": memoryExtractionSchema,
			},
		},
	}

	reqBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request with timeout
	httpReq, err := http.NewRequestWithContext(ctx, "POST", provider.BaseURL+"/chat/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	// Send request with 60s timeout
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("⚠️ [MEMORY-EXTRACTION] API error: %s", string(body))
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return nil, fmt.Errorf("no response from extractor model")
	}

	// Parse the extraction result
	var result models.ExtractedMemoryFromLLM
	content := apiResponse.Choices[0].Message.Content

	if err := json.Unmarshal([]byte(content), &result); err != nil {
		// SECURITY: Don't log decrypted content - only log length
		log.Printf("⚠️ [MEMORY-EXTRACTION] Failed to parse extraction: %v (response length: %d bytes)", err, len(content))
		return nil, fmt.Errorf("failed to parse extraction: %w", err)
	}

	return &result, nil
}

// calculateEngagement calculates conversation engagement score
func (s *MemoryExtractionService) calculateEngagement(messages []map[string]interface{}) float64 {
	if len(messages) == 0 {
		return 0.0
	}

	userMessageCount := 0
	totalResponseLength := 0

	for _, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)

		if role == "user" {
			userMessageCount++
			totalResponseLength += len(content)
		}
	}

	if userMessageCount == 0 {
		return 0.0
	}

	// Turn ratio (how much user participated)
	turnRatio := float64(userMessageCount) / float64(len(messages))

	// Length score (longer responses = more engaged)
	avgUserLength := totalResponseLength / userMessageCount
	lengthScore := float64(avgUserLength) / 200.0
	if lengthScore > 1.0 {
		lengthScore = 1.0
	}

	// Recency bonus (recent conversations get boost)
	recencyBonus := 1.0 // Assume all conversations being extracted are recent

	// Weighted engagement score
	engagement := (0.5 * turnRatio) + (0.3 * lengthScore) + (0.2 * recencyBonus)

	log.Printf("📊 [MEMORY-EXTRACTION] Engagement: %.2f (turn: %.2f, length: %.2f)", engagement, turnRatio, lengthScore)

	return engagement
}

// storeEngagement stores conversation engagement in database
func (s *MemoryExtractionService) storeEngagement(
	ctx context.Context,
	userID string,
	conversationID string,
	messages []map[string]interface{},
	engagementScore float64,
) error {

	userMessageCount := 0
	totalResponseLength := 0

	for _, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)

		if role == "user" {
			userMessageCount++
			totalResponseLength += len(content)
		}
	}

	avgResponseLength := 0
	if userMessageCount > 0 {
		avgResponseLength = totalResponseLength / userMessageCount
	}

	engagement := &models.ConversationEngagement{
		ID:                primitive.NewObjectID(),
		UserID:            userID,
		ConversationID:    conversationID,
		MessageCount:      len(messages),
		UserMessageCount:  userMessageCount,
		AvgResponseLength: avgResponseLength,
		EngagementScore:   engagementScore,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	// Upsert (update if exists, insert if not)
	filter := bson.M{"userId": userID, "conversationId": conversationID}
	update := bson.M{
		"$set": bson.M{
			"messageCount":      engagement.MessageCount,
			"userMessageCount":  engagement.UserMessageCount,
			"avgResponseLength": engagement.AvgResponseLength,
			"engagementScore":   engagement.EngagementScore,
			"updatedAt":         engagement.UpdatedAt,
		},
		"$setOnInsert": bson.M{
			"_id":       engagement.ID,
			"createdAt": engagement.CreatedAt,
		},
	}

	opts := options.Update().SetUpsert(true)
	_, err := s.engagementCollection.UpdateOne(ctx, filter, update, opts)

	return err
}

// buildConversationTranscript builds a human-readable transcript
func (s *MemoryExtractionService) buildConversationTranscript(messages []map[string]interface{}) string {
	var builder strings.Builder

	for _, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)

		// Skip system messages
		if role == "system" {
			continue
		}

		// Format message
		if role == "user" {
			builder.WriteString(fmt.Sprintf("USER: %s\n\n", content))
		} else if role == "assistant" {
			builder.WriteString(fmt.Sprintf("ASSISTANT: %s\n\n", content))
		}
	}

	return builder.String()
}

// buildExistingMemoriesContext formats existing memories for LLM context
func (s *MemoryExtractionService) buildExistingMemoriesContext(
	ctx context.Context,
	userID string,
	memories []models.DecryptedMemory,
) string {
	if len(memories) == 0 {
		return "(No existing memories - this is the first extraction)"
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("(%d existing memories):\n\n", len(memories)))

	for i, mem := range memories {
		// Format: [category] content (tags: tag1, tag2)
		tags := ""
		if len(mem.Tags) > 0 {
			tags = fmt.Sprintf(" (tags: %s)", strings.Join(mem.Tags, ", "))
		}

		builder.WriteString(fmt.Sprintf("%d. [%s] %s%s\n", i+1, mem.Category, mem.DecryptedContent, tags))
	}

	return builder.String()
}

// getExtractorModelForUser gets user's preferred extractor model
func (s *MemoryExtractionService) getExtractorModelForUser(ctx context.Context, userID string) (string, error) {
	// Check system-wide setting first (admin override)
	if s.settingsService != nil {
		assignments, err := s.settingsService.GetSystemModelAssignments(ctx)
		if err == nil && assignments.MemoryExtractor != "" {
			log.Printf("🎯 [MEMORY-EXTRACTION] Using system-assigned model: %s", assignments.MemoryExtractor)
			return assignments.MemoryExtractor, nil
		}
	}

	// Fall back to user preference
	// Query MongoDB for user preferences
	usersCollection := s.mongodb.Collection(database.CollectionUsers)

	var user models.User
	err := usersCollection.FindOne(ctx, bson.M{"supabaseUserId": userID}).Decode(&user)
	if err != nil {
		return "", nil // No user found, return empty (will use pool)
	}

	// If user has a preference and it's not empty, use it
	if user.Preferences.MemoryExtractorModelID != "" {
		log.Printf("🎯 [MEMORY-EXTRACTION] Using user-preferred model: %s", user.Preferences.MemoryExtractorModelID)
		return user.Preferences.MemoryExtractorModelID, nil
	}

	// No preference set - return empty (will use pool)
	return "", nil
}

// getProviderAndModel resolves model ID to provider and actual model name
func (s *MemoryExtractionService) getProviderAndModel(modelID string) (*models.Provider, string, error) {
	if modelID == "" {
		return nil, "", fmt.Errorf("model ID is required")
	}

	// Try to resolve through ChatService (handles aliases)
	if s.chatService != nil {
		if provider, actualModel, found := s.chatService.ResolveModelAlias(modelID); found {
			return provider, actualModel, nil
		}
	}

	return nil, "", fmt.Errorf("model %s not found in providers", modelID)
}

// updateJobStatus updates job status
func (s *MemoryExtractionService) updateJobStatus(ctx context.Context, jobID primitive.ObjectID, status string) {
	update := bson.M{
		"$set": bson.M{
			"status": status,
		},
	}
	s.jobCollection.UpdateOne(ctx, bson.M{"_id": jobID}, update)
}

// markJobCompleted marks job as completed
func (s *MemoryExtractionService) markJobCompleted(ctx context.Context, jobID primitive.ObjectID) {
	now := time.Now()
	update := bson.M{
		"$set": bson.M{
			"status":      models.JobStatusCompleted,
			"processedAt": now,
		},
	}
	s.jobCollection.UpdateOne(ctx, bson.M{"_id": jobID}, update)
}

// markJobFailed marks job as failed with error message
func (s *MemoryExtractionService) markJobFailed(ctx context.Context, jobID primitive.ObjectID, errorMsg string) {
	update := bson.M{
		"$set": bson.M{
			"status":       models.JobStatusFailed,
			"errorMessage": errorMsg,
		},
		"$inc": bson.M{
			"attemptCount": 1,
		},
	}
	s.jobCollection.UpdateOne(ctx, bson.M{"_id": jobID}, update)
}

// Helper function for pointer
