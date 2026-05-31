package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// MongoDB wraps the MongoDB client and database
type MongoDB struct {
	client   *mongo.Client
	database *mongo.Database
	dbName   string
}

// Collection names
const (
	CollectionUsers                = "users"
	CollectionAgents               = "agents"
	CollectionWorkflows            = "workflows"
	CollectionBuilderConversations = "builder_conversations"
	CollectionExecutions           = "executions"
	CollectionProviders            = "providers"
	CollectionModels               = "models"
	CollectionMCPConnections       = "mcp_connections"
	CollectionMCPTools             = "mcp_tools"
	CollectionMCPAuditLog          = "mcp_audit_log"
	CollectionCredentials          = "credentials"
	CollectionChats                = "chats"

	// Memory system collections
	CollectionMemories                = "memories"
	CollectionMemoryExtractionJobs    = "memory_extraction_jobs"
	CollectionConversationEngagement  = "conversation_engagement"

	// Skills system collections
	CollectionSkills     = "skills"
	CollectionUserSkills = "user_skills"

	// Nexus multi-agent system collections
	CollectionNexusTasks           = "nexus_tasks"
	CollectionNexusDaemons         = "nexus_daemons"
	CollectionNexusPersona         = "nexus_persona"
	CollectionNexusSessions        = "nexus_sessions"
	CollectionNexusEngrams         = "nexus_engrams"
	CollectionNexusDaemonTemplates = "nexus_daemon_templates"
	CollectionNexusProjects        = "nexus_projects"
	CollectionNexusSaves           = "nexus_saves"
)

// NewMongoDB creates a new MongoDB connection with connection pooling
func NewMongoDB(uri string) (*MongoDB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Configure client options with connection pooling
	clientOptions := options.Client().
		ApplyURI(uri).
		SetMaxPoolSize(50).
		SetMinPoolSize(5).
		SetMaxConnIdleTime(30 * time.Second).
		SetServerSelectionTimeout(5 * time.Second).
		SetConnectTimeout(10 * time.Second)

	// Connect to MongoDB
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	// Ping to verify connection
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return nil, fmt.Errorf("failed to ping MongoDB: %w", err)
	}

	// Extract database name from URI or use default
	dbName := extractDBName(uri)
	if dbName == "" {
		dbName = "claraverse"
	}

	db := &MongoDB{
		client:   client,
		database: client.Database(dbName),
		dbName:   dbName,
	}

	log.Printf("✅ Connected to MongoDB database: %s", dbName)

	return db, nil
}

// extractDBName extracts the database name from MongoDB URI
func extractDBName(uri string) string {
	// Extract database name from URI path component
	// mongodb://localhost:27017/claraverse?authSource=admin -> claraverse
	// mongodb+srv://user:pass@cluster/claraverse -> claraverse

	// Find the database name between the last "/" and "?" or end of string
	lastSlash := -1
	questionMark := -1

	for i, c := range uri {
		if c == '/' {
			lastSlash = i
		}
		if c == '?' && questionMark == -1 {
			questionMark = i
		}
	}

	if lastSlash != -1 {
		start := lastSlash + 1
		end := len(uri)
		if questionMark != -1 && questionMark > lastSlash {
			end = questionMark
		}
		if start < end {
			dbName := uri[start:end]
			if dbName != "" {
				return dbName
			}
		}
	}

	// Default fallback
	return "claraverse"
}

// Initialize creates indexes for all collections
func (m *MongoDB) Initialize(ctx context.Context) error {
	log.Println("📦 Initializing MongoDB indexes...")

	// Users collection indexes
	if err := m.createIndexes(ctx, CollectionUsers, []mongo.IndexModel{
		{Keys: bson.D{{Key: "supabaseUserId", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "email", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("failed to create users indexes: %w", err)
	}

	// Agents collection indexes
	if err := m.createIndexes(ctx, CollectionAgents, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "updatedAt", Value: -1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("failed to create agents indexes: %w", err)
	}

	// Workflows collection indexes
	if err := m.createIndexes(ctx, CollectionWorkflows, []mongo.IndexModel{
		{Keys: bson.D{{Key: "agentId", Value: 1}, {Key: "version", Value: -1}}},
	}); err != nil {
		return fmt.Errorf("failed to create workflows indexes: %w", err)
	}

	// Builder conversations collection indexes
	if err := m.createIndexes(ctx, CollectionBuilderConversations, []mongo.IndexModel{
		{Keys: bson.D{{Key: "agentId", Value: 1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}}},
		{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
	}); err != nil {
		return fmt.Errorf("failed to create builder_conversations indexes: %w", err)
	}

	// Executions collection indexes
	if err := m.createIndexes(ctx, CollectionExecutions, []mongo.IndexModel{
		{Keys: bson.D{{Key: "agentId", Value: 1}, {Key: "startedAt", Value: -1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("failed to create executions indexes: %w", err)
	}

	// Providers collection indexes
	if err := m.createIndexes(ctx, CollectionProviders, []mongo.IndexModel{
		{Keys: bson.D{{Key: "name", Value: 1}}, Options: options.Index().SetUnique(true)},
	}); err != nil {
		return fmt.Errorf("failed to create providers indexes: %w", err)
	}

	// Models collection indexes
	if err := m.createIndexes(ctx, CollectionModels, []mongo.IndexModel{
		{Keys: bson.D{{Key: "providerId", Value: 1}}},
		{Keys: bson.D{{Key: "isVisible", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("failed to create models indexes: %w", err)
	}

	// MCP connections indexes
	if err := m.createIndexes(ctx, CollectionMCPConnections, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "isActive", Value: 1}}},
		{Keys: bson.D{{Key: "clientId", Value: 1}}, Options: options.Index().SetUnique(true)},
	}); err != nil {
		return fmt.Errorf("failed to create mcp_connections indexes: %w", err)
	}

	// MCP tools indexes
	if err := m.createIndexes(ctx, CollectionMCPTools, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "toolName", Value: 1}}, Options: options.Index().SetUnique(true)},
	}); err != nil {
		return fmt.Errorf("failed to create mcp_tools indexes: %w", err)
	}

	// MCP audit log indexes
	if err := m.createIndexes(ctx, CollectionMCPAuditLog, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "executedAt", Value: -1}}},
	}); err != nil {
		return fmt.Errorf("failed to create mcp_audit_log indexes: %w", err)
	}

	// Chats collection indexes (for cloud sync)
	if err := m.createIndexes(ctx, CollectionChats, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "updatedAt", Value: -1}}}, // List user's chats sorted by recent
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "chatId", Value: 1}}, Options: options.Index().SetUnique(true)}, // Unique chat per user
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "isStarred", Value: 1}}}, // Filter starred chats
	}); err != nil {
		return fmt.Errorf("failed to create chats indexes: %w", err)
	}

	// Memories collection indexes
	if err := m.createIndexes(ctx, CollectionMemories, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "isArchived", Value: 1}, {Key: "score", Value: -1}}}, // Get top active memories
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "contentHash", Value: 1}}, Options: options.Index().SetUnique(true)}, // Deduplication
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "category", Value: 1}}}, // Filter by category
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "tags", Value: 1}}}, // Tag-based lookup
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "lastAccessedAt", Value: -1}}}, // Recency tracking
	}); err != nil {
		return fmt.Errorf("failed to create memories indexes: %w", err)
	}

	// Memory extraction jobs collection indexes
	if err := m.createIndexes(ctx, CollectionMemoryExtractionJobs, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "status", Value: 1}}}, // Find pending jobs
		{Keys: bson.D{{Key: "createdAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(86400)}, // TTL: cleanup after 24h
	}); err != nil {
		return fmt.Errorf("failed to create memory_extraction_jobs indexes: %w", err)
	}

	// Conversation engagement collection indexes
	if err := m.createIndexes(ctx, CollectionConversationEngagement, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "conversationId", Value: 1}}, Options: options.Index().SetUnique(true)}, // Unique per user+conversation
	}); err != nil {
		return fmt.Errorf("failed to create conversation_engagement indexes: %w", err)
	}

	// Nexus tasks collection indexes (all in one call)
	if err := m.createIndexes(ctx, CollectionNexusTasks, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "createdAt", Value: -1}}},
		{Keys: bson.D{{Key: "sessionId", Value: 1}, {Key: "createdAt", Value: -1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "projectId", Value: 1}, {Key: "status", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("failed to create nexus_tasks indexes: %w", err)
	}

	// Nexus daemons collection indexes
	if err := m.createIndexes(ctx, CollectionNexusDaemons, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "taskId", Value: 1}}},
		{Keys: bson.D{{Key: "sessionId", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("failed to create nexus_daemons indexes: %w", err)
	}

	// Nexus persona collection indexes
	if err := m.createIndexes(ctx, CollectionNexusPersona, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "category", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("failed to create nexus_persona indexes: %w", err)
	}

	// Nexus sessions collection indexes
	if err := m.createIndexes(ctx, CollectionNexusSessions, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}}, Options: options.Index().SetUnique(true)},
	}); err != nil {
		return fmt.Errorf("failed to create nexus_sessions indexes: %w", err)
	}

	// Nexus engrams collection indexes
	if err := m.createIndexes(ctx, CollectionNexusEngrams, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "type", Value: 1}}},
		{Keys: bson.D{{Key: "sessionId", Value: 1}, {Key: "createdAt", Value: -1}}},
		{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
	}); err != nil {
		return fmt.Errorf("failed to create nexus_engrams indexes: %w", err)
	}

	// Nexus daemon templates collection indexes
	if err := m.createIndexes(ctx, CollectionNexusDaemonTemplates, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "isActive", Value: 1}}},
		{Keys: bson.D{{Key: "isDefault", Value: 1}}},
		{Keys: bson.D{{Key: "slug", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("failed to create nexus_daemon_templates indexes: %w", err)
	}

	// Nexus projects collection indexes
	if err := m.createIndexes(ctx, CollectionNexusProjects, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "isArchived", Value: 1}, {Key: "sortOrder", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("failed to create nexus_projects indexes: %w", err)
	}

	// Nexus saves collection indexes
	if err := m.createIndexes(ctx, CollectionNexusSaves, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "createdAt", Value: -1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "tags", Value: 1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "sourceTaskId", Value: 1}}},
	}); err != nil {
		return fmt.Errorf("failed to create nexus_saves indexes: %w", err)
	}

	log.Println("✅ MongoDB indexes initialized successfully")
	return nil
}

// createIndexes creates indexes for a collection
func (m *MongoDB) createIndexes(ctx context.Context, collectionName string, indexes []mongo.IndexModel) error {
	collection := m.database.Collection(collectionName)
	_, err := collection.Indexes().CreateMany(ctx, indexes)
	return err
}

// Collection returns a collection handle
func (m *MongoDB) Collection(name string) *mongo.Collection {
	return m.database.Collection(name)
}

// Client returns the underlying MongoDB client
func (m *MongoDB) Client() *mongo.Client {
	return m.client
}

// Database returns the underlying MongoDB database
func (m *MongoDB) Database() *mongo.Database {
	return m.database
}

// Close closes the MongoDB connection
func (m *MongoDB) Close(ctx context.Context) error {
	log.Println("🔌 Closing MongoDB connection...")
	return m.client.Disconnect(ctx)
}

// Ping checks if the database connection is alive
func (m *MongoDB) Ping(ctx context.Context) error {
	return m.client.Ping(ctx, readpref.Primary())
}

// WithTransaction executes a function within a transaction
func (m *MongoDB) WithTransaction(ctx context.Context, fn func(sessCtx mongo.SessionContext) error) error {
	session, err := m.client.StartSession()
	if err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}
	defer session.EndSession(ctx)

	_, err = session.WithTransaction(ctx, func(sessCtx mongo.SessionContext) (interface{}, error) {
		return nil, fn(sessCtx)
	})
	return err
}
