package services

import (
	"claraverse/internal/database"
	"claraverse/internal/models"
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/robfig/cron/v3"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// RoutineService manages Dobby's Claw routines
type RoutineService struct {
	mongoDB      *database.MongoDB
	redisService *RedisService
	scheduler    gocron.Scheduler
	mu           sync.RWMutex
	jobs         map[string]gocron.Job // routineID -> job

	// Late-bound dependencies
	channelService *ChannelService
	chatService    *ChatService
	mcpService     *MCPBridgeService
	toolService    *ToolService
	cortexService  *CortexService
}

// NewRoutineService creates a new routine service
func NewRoutineService(mongoDB *database.MongoDB, redisService *RedisService) (*RoutineService, error) {
	scheduler, err := gocron.NewScheduler(gocron.WithLocation(time.UTC))
	if err != nil {
		return nil, fmt.Errorf("failed to create routine scheduler: %w", err)
	}

	return &RoutineService{
		mongoDB:      mongoDB,
		redisService: redisService,
		scheduler:    scheduler,
		jobs:         make(map[string]gocron.Job),
	}, nil
}

// SetChannelService sets the channel service for Telegram delivery
func (s *RoutineService) SetChannelService(svc *ChannelService) {
	s.channelService = svc
}

// SetChatService sets the chat service for agent execution
func (s *RoutineService) SetChatService(svc *ChatService) {
	s.chatService = svc
}

// SetMCPBridgeService sets the MCP bridge service for tool access
func (s *RoutineService) SetMCPBridgeService(svc *MCPBridgeService) {
	s.mcpService = svc
}

// SetToolService sets the tool service for credential-filtered tool definitions
func (s *RoutineService) SetToolService(svc *ToolService) {
	s.toolService = svc
}

// SetCortexService sets the Cortex orchestrator for Nexus-powered routine execution
func (s *RoutineService) SetCortexService(svc *CortexService) {
	s.cortexService = svc
}

// Start loads all enabled routines and starts the scheduler
func (s *RoutineService) Start(ctx context.Context) error {
	log.Println("⏰ Starting routine service...")

	if s.mongoDB == nil {
		log.Println("⚠️ MongoDB not available, skipping routine loading")
		return nil
	}

	// Ensure indexes
	collection := s.collection()
	_, _ = collection.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "enabled", Value: 1}}},
	})

	// Load enabled routines
	cursor, err := collection.Find(ctx, bson.M{"enabled": true})
	if err != nil {
		return fmt.Errorf("failed to query routines: %w", err)
	}
	defer cursor.Close(ctx)

	var count int
	for cursor.Next(ctx) {
		var routine models.Routine
		if err := cursor.Decode(&routine); err != nil {
			log.Printf("⚠️ Failed to decode routine: %v", err)
			continue
		}
		if err := s.registerJob(&routine); err != nil {
			log.Printf("⚠️ Failed to register routine %s: %v", routine.ID.Hex(), err)
			continue
		}
		count++
	}

	s.scheduler.Start()
	log.Printf("✅ Routine service started (%d routines loaded)", count)
	return nil
}

// Stop stops the routine scheduler
func (s *RoutineService) Stop() error {
	log.Println("⏹️ Stopping routine service...")
	return s.scheduler.Shutdown()
}

// Create creates a new routine
func (s *RoutineService) Create(ctx context.Context, userID string, req *models.CreateRoutineRequest) (*models.Routine, error) {
	// Validate cron expression
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(req.CronExpression); err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}

	// Validate timezone
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		return nil, fmt.Errorf("invalid timezone: %w", err)
	}

	now := time.Now()
	routine := &models.Routine{
		UserID:         userID,
		Name:           req.Name,
		Prompt:         req.Prompt,
		CronExpression: req.CronExpression,
		Timezone:       req.Timezone,
		Enabled:        true,
		DeliveryMethod: req.DeliveryMethod,
		ModelID:        req.ModelID,
		EnabledTools:   req.EnabledTools,
		Template:       req.Template,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	result, err := s.collection().InsertOne(ctx, routine)
	if err != nil {
		return nil, fmt.Errorf("failed to create routine: %w", err)
	}

	routine.ID = result.InsertedID.(primitive.ObjectID)

	// Register with scheduler
	if err := s.registerJob(routine); err != nil {
		log.Printf("⚠️ Failed to register routine job %s: %v", routine.ID.Hex(), err)
	}

	return routine, nil
}

// Update updates an existing routine
func (s *RoutineService) Update(ctx context.Context, userID string, routineID string, req *models.UpdateRoutineRequest) (*models.Routine, error) {
	objID, err := primitive.ObjectIDFromHex(routineID)
	if err != nil {
		return nil, fmt.Errorf("invalid routine ID: %w", err)
	}

	update := bson.M{"updatedAt": time.Now()}
	if req.Name != nil {
		update["name"] = *req.Name
	}
	if req.Prompt != nil {
		update["prompt"] = *req.Prompt
	}
	if req.CronExpression != nil {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(*req.CronExpression); err != nil {
			return nil, fmt.Errorf("invalid cron expression: %w", err)
		}
		update["cronExpression"] = *req.CronExpression
	}
	if req.Timezone != nil {
		if _, err := time.LoadLocation(*req.Timezone); err != nil {
			return nil, fmt.Errorf("invalid timezone: %w", err)
		}
		update["timezone"] = *req.Timezone
	}
	if req.Enabled != nil {
		update["enabled"] = *req.Enabled
	}
	if req.DeliveryMethod != nil {
		update["deliveryMethod"] = *req.DeliveryMethod
	}
	if req.ModelID != nil {
		update["modelId"] = *req.ModelID
	}
	if req.EnabledTools != nil {
		update["enabledTools"] = req.EnabledTools
	}

	filter := bson.M{"_id": objID, "userId": userID}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var routine models.Routine
	err = s.collection().FindOneAndUpdate(ctx, filter, bson.M{"$set": update}, opts).Decode(&routine)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("routine not found")
		}
		return nil, fmt.Errorf("failed to update routine: %w", err)
	}

	// Re-register with scheduler if schedule changed or enabled/disabled
	_ = s.unregisterJob(routineID)
	if routine.Enabled {
		if err := s.registerJob(&routine); err != nil {
			log.Printf("⚠️ Failed to re-register routine job %s: %v", routineID, err)
		}
	}

	return &routine, nil
}

// Delete removes a routine
func (s *RoutineService) Delete(ctx context.Context, userID string, routineID string) error {
	objID, err := primitive.ObjectIDFromHex(routineID)
	if err != nil {
		return fmt.Errorf("invalid routine ID: %w", err)
	}

	result, err := s.collection().DeleteOne(ctx, bson.M{"_id": objID, "userId": userID})
	if err != nil {
		return fmt.Errorf("failed to delete routine: %w", err)
	}
	if result.DeletedCount == 0 {
		return fmt.Errorf("routine not found")
	}

	_ = s.unregisterJob(routineID)
	return nil
}

// List returns all routines for a user
func (s *RoutineService) List(ctx context.Context, userID string) ([]*models.Routine, error) {
	cursor, err := s.collection().Find(ctx, bson.M{"userId": userID}, options.Find().SetSort(bson.M{"createdAt": -1}))
	if err != nil {
		return nil, fmt.Errorf("failed to list routines: %w", err)
	}
	defer cursor.Close(ctx)

	var routines []*models.Routine
	if err := cursor.All(ctx, &routines); err != nil {
		return nil, fmt.Errorf("failed to decode routines: %w", err)
	}

	return routines, nil
}

// GetByID returns a single routine
func (s *RoutineService) GetByID(ctx context.Context, userID string, routineID string) (*models.Routine, error) {
	objID, err := primitive.ObjectIDFromHex(routineID)
	if err != nil {
		return nil, fmt.Errorf("invalid routine ID: %w", err)
	}

	var routine models.Routine
	err = s.collection().FindOne(ctx, bson.M{"_id": objID, "userId": userID}).Decode(&routine)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("routine not found")
		}
		return nil, fmt.Errorf("failed to get routine: %w", err)
	}

	return &routine, nil
}

// Trigger executes a routine immediately
func (s *RoutineService) Trigger(ctx context.Context, userID string, routineID string) error {
	routine, err := s.GetByID(ctx, userID, routineID)
	if err != nil {
		return err
	}

	go s.executeRoutine(routine)
	return nil
}

// TestRoutine runs a routine prompt synchronously with tools and returns the result (no save, no delivery)
func (s *RoutineService) TestRoutine(ctx context.Context, userID string, req *models.TestRoutineRequest) (string, error) {
	if s.chatService == nil {
		return "", fmt.Errorf("chat service not available")
	}

	name := req.Name
	if name == "" {
		name = "Test Routine"
	}

	systemPrompt := fmt.Sprintf(
		"You are Dobby, an AI assistant executing a scheduled routine. "+
			"Execute the following task and provide a clear, concise result. "+
			"The current time is %s. Routine name: %s.",
		time.Now().Format(time.RFC1123),
		name,
	)

	messages := []map[string]interface{}{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": req.Prompt},
	}

	// Gather available tool definitions for the user
	var availableTools []map[string]interface{}
	if len(req.EnabledTools) > 0 && s.toolService != nil {
		allTools := s.toolService.GetAvailableToolsWithMCP(ctx, userID)

		// Filter to only the tools the routine has selected
		selectedSet := make(map[string]bool, len(req.EnabledTools))
		for _, name := range req.EnabledTools {
			selectedSet[name] = true
		}

		for _, toolDef := range allTools {
			if fn, ok := toolDef["function"].(map[string]interface{}); ok {
				if toolName, ok := fn["name"].(string); ok && selectedSet[toolName] {
					availableTools = append(availableTools, toolDef)
				}
			}
		}

		log.Printf("🧪 [ROUTINE-TEST] Tools for test: %d/%d (selected: %v)", len(availableTools), len(allTools), req.EnabledTools)
	}

	// If we have tools, use the tool-calling loop; otherwise simple completion
	if len(availableTools) > 0 {
		result, err := s.chatService.ChatCompletionWithTools(ctx, userID, "", req.ModelID, messages, availableTools, 10)
		if err != nil {
			return "", fmt.Errorf("test execution failed: %w", err)
		}
		return result, nil
	}

	// No tools — simple completion
	result, err := s.chatService.ChatCompletionSync(ctx, userID, req.ModelID, messages, nil)
	if err != nil {
		return "", fmt.Errorf("test execution failed: %w", err)
	}
	return result, nil
}

// GetUserStatus returns the combined Dobby's Claw status for a user
func (s *RoutineService) GetUserStatus(ctx context.Context, userID string, channelSvc *ChannelService, mcpSvc *MCPBridgeService) map[string]interface{} {
	status := map[string]interface{}{
		"setupComplete": false,
	}

	// Telegram status
	telegramStatus := map[string]interface{}{
		"connected": false,
	}
	if channelSvc != nil {
		channels, err := channelSvc.ListByUser(ctx, userID)
		if err == nil {
			for _, ch := range channels {
				if ch.Platform == models.ChannelPlatformTelegram && ch.Enabled {
					telegramStatus["connected"] = true
					telegramStatus["botUsername"] = ch.BotUsername
					telegramStatus["botName"] = ch.BotName
					break
				}
			}
		}
	}
	status["telegram"] = telegramStatus

	// MCP status
	mcpStatus := map[string]interface{}{
		"connected": false,
		"toolCount": 0,
		"servers":   []interface{}{},
	}
	if mcpSvc != nil {
		conn, exists := mcpSvc.GetUserConnection(userID)
		if exists && conn != nil && conn.IsActive {
			mcpStatus["connected"] = true
			mcpStatus["platform"] = conn.Platform
			mcpStatus["toolCount"] = len(conn.Tools)
			// Return server configs sent by the bridge client during registration
			if len(conn.Servers) > 0 {
				mcpStatus["servers"] = conn.Servers
			}
		}
	}
	status["mcp"] = mcpStatus

	// Routines status
	routineStatus := map[string]interface{}{
		"total":  0,
		"active": 0,
	}
	if s.mongoDB != nil {
		routines, err := s.List(ctx, userID)
		if err == nil {
			routineStatus["total"] = len(routines)
			active := 0
			for _, r := range routines {
				if r.Enabled {
					active++
				}
			}
			routineStatus["active"] = active
		}
	}
	status["routines"] = routineStatus

	// Setup is complete if at least one connection exists
	telegramConn, _ := telegramStatus["connected"].(bool)
	mcpConn, _ := mcpStatus["connected"].(bool)
	status["setupComplete"] = telegramConn || mcpConn

	return status
}

// registerJob registers a routine with the gocron scheduler
func (s *RoutineService) registerJob(routine *models.Routine) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tz := routine.Timezone
	if tz == "" {
		tz = "UTC"
	}

	cronWithTZ := fmt.Sprintf("CRON_TZ=%s %s", tz, routine.CronExpression)

	routineCopy := *routine // Copy to avoid closure issues
	job, err := s.scheduler.NewJob(
		gocron.CronJob(cronWithTZ, false),
		gocron.NewTask(func() {
			s.executeRoutine(&routineCopy)
		}),
		gocron.WithName("routine_"+routine.ID.Hex()),
	)
	if err != nil {
		return fmt.Errorf("failed to create routine job: %w", err)
	}

	s.jobs[routine.ID.Hex()] = job
	log.Printf("📅 Registered routine %s: %s (cron: %s)", routine.ID.Hex(), routine.Name, routine.CronExpression)
	return nil
}

// unregisterJob removes a routine from the scheduler
func (s *RoutineService) unregisterJob(routineID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, exists := s.jobs[routineID]
	if !exists {
		return nil
	}

	if err := s.scheduler.RemoveJob(job.ID()); err != nil {
		return fmt.Errorf("failed to remove routine job: %w", err)
	}

	delete(s.jobs, routineID)
	return nil
}

// executeRoutine runs the routine agent loop and delivers results
func (s *RoutineService) executeRoutine(routine *models.Routine) {
	ctx := context.Background()
	startTime := time.Now()

	log.Printf("▶️ Executing routine %s: %s", routine.ID.Hex(), routine.Name)

	// Distributed lock to prevent duplicate runs
	if s.redisService != nil {
		lockKey := fmt.Sprintf("routine-lock:%s:%d", routine.ID.Hex(), time.Now().Unix()/60)
		acquired, err := s.redisService.AcquireLock(ctx, lockKey, "routine", 5*time.Minute)
		if err != nil || !acquired {
			log.Printf("⏭️ Routine %s already being executed", routine.ID.Hex())
			return
		}
		defer func() {
			_, _ = s.redisService.ReleaseLock(ctx, lockKey, "routine")
		}()
	}

	var resultText string
	var success bool

	// Route through Cortex if available (enables multi-agent execution for routines)
	if s.cortexService != nil {
		result, err := s.cortexService.HandleRoutineSync(ctx, routine.UserID, routine.Prompt, routine.ModelID, routine.ID)
		if err != nil {
			resultText = fmt.Sprintf("Routine failed (Cortex): %v", err)
			success = false
		} else {
			resultText = result
			success = true
		}
	} else if s.chatService != nil {
		// Build system prompt for routine execution
		systemPrompt := fmt.Sprintf(
			"You are Dobby, an AI assistant executing a scheduled routine. "+
				"Execute the following task and provide a clear, concise result. "+
				"The current time is %s. Routine name: %s.",
			time.Now().Format(time.RFC1123),
			routine.Name,
		)

		messages := []map[string]interface{}{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": routine.Prompt},
		}

		// Gather tools if the routine has enabled tools
		var availableTools []map[string]interface{}
		if len(routine.EnabledTools) > 0 && s.toolService != nil {
			allTools := s.toolService.GetAvailableToolsWithMCP(ctx, routine.UserID)
			selectedSet := make(map[string]bool, len(routine.EnabledTools))
			for _, name := range routine.EnabledTools {
				selectedSet[name] = true
			}
			for _, toolDef := range allTools {
				if fn, ok := toolDef["function"].(map[string]interface{}); ok {
					if toolName, ok := fn["name"].(string); ok && selectedSet[toolName] {
						availableTools = append(availableTools, toolDef)
					}
				}
			}
			log.Printf("📅 [ROUTINE] Routine %s has %d/%d tools available", routine.ID.Hex(), len(availableTools), len(allTools))
		}

		// Use tool-calling loop if tools are available, otherwise simple completion
		if len(availableTools) > 0 {
			result, err := s.chatService.ChatCompletionWithTools(ctx, routine.UserID, "", routine.ModelID, messages, availableTools, 10)
			if err != nil {
				resultText = fmt.Sprintf("Routine failed: %v", err)
				success = false
			} else {
				resultText = result
				success = true
			}
		} else {
			result, err := s.chatService.ChatCompletionSync(ctx, routine.UserID, routine.ModelID, messages, nil)
			if err != nil {
				resultText = fmt.Sprintf("Routine failed: %v", err)
				success = false
			} else {
				resultText = result
				success = true
			}
		}
	} else {
		resultText = "Chat service not available"
		success = false
	}

	// Update stats
	now := time.Now()
	updateDoc := bson.M{
		"lastRunAt":  now,
		"lastResult": resultText,
		"updatedAt":  now,
	}
	if success {
		updateDoc["$inc"] = bson.M{"totalRuns": 1, "successfulRuns": 1}
	} else {
		updateDoc["$inc"] = bson.M{"totalRuns": 1, "failedRuns": 1}
	}

	// Use $set for non-increment fields, $inc for counters
	_, _ = s.collection().UpdateOne(ctx, bson.M{"_id": routine.ID}, bson.M{
		"$set": bson.M{
			"lastRunAt":  now,
			"lastResult": resultText,
			"updatedAt":  now,
		},
		"$inc": bson.M{
			"totalRuns":      int64(1),
			"successfulRuns": map[bool]int64{true: 1, false: 0}[success],
			"failedRuns":     map[bool]int64{true: 0, false: 1}[success],
		},
	})

	// Deliver result
	if routine.DeliveryMethod == "telegram" && s.channelService != nil && success {
		s.deliverViaTelegram(ctx, routine.UserID, routine.Name, resultText)
	}

	elapsed := time.Since(startTime)
	log.Printf("✅ Routine %s completed in %v (success: %v)", routine.ID.Hex(), elapsed, success)
}

// deliverViaTelegram sends the routine result to the user's Telegram bot
func (s *RoutineService) deliverViaTelegram(ctx context.Context, userID string, routineName string, result string) {
	if s.channelService == nil {
		return
	}

	// Get user's Telegram channel
	channel, err := s.channelService.GetByUserAndPlatform(ctx, userID, models.ChannelPlatformTelegram)
	if err != nil || channel == nil || !channel.Enabled {
		log.Printf("⚠️ No active Telegram channel for user %s", userID)
		return
	}

	// Get decrypted config to extract bot token
	config, err := s.channelService.GetDecryptedConfig(ctx, channel)
	if err != nil {
		log.Printf("⚠️ Failed to decrypt channel config: %v", err)
		return
	}

	botToken, _ := config["bot_token"].(string)
	if botToken == "" {
		log.Printf("⚠️ No bot token in channel config for user %s", userID)
		return
	}

	message := fmt.Sprintf("📋 *%s*\n\n%s", routineName, result)

	// Get the most recent session to find chat ID
	// PlatformChatID is stored as string in the session model — need to parse to int64
	sessionsCollection := s.mongoDB.Database().Collection("channel_sessions")
	var session struct {
		PlatformChatID string `bson:"platformChatId"`
	}
	err = sessionsCollection.FindOne(ctx, bson.M{
		"channelId": channel.ID,
	}, options.FindOne().SetSort(bson.M{"lastActivityAt": -1})).Decode(&session)
	if err != nil {
		log.Printf("⚠️ No active session for Telegram delivery (user %s): %v", userID, err)
		return
	}

	chatID, err := strconv.ParseInt(session.PlatformChatID, 10, 64)
	if err != nil {
		log.Printf("⚠️ Invalid chat ID '%s' for Telegram delivery (user %s): %v", session.PlatformChatID, userID, err)
		return
	}

	if err := s.channelService.SendTelegramMessageChunked(ctx, botToken, chatID, message); err != nil {
		log.Printf("⚠️ Failed to deliver routine result via Telegram: %v", err)
	} else {
		log.Printf("📬 [ROUTINE] Delivered result to Telegram for user %s (chat %d)", userID, chatID)
	}
}

func (s *RoutineService) collection() *mongo.Collection {
	return s.mongoDB.Database().Collection("routines")
}
