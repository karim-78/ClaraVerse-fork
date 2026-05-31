package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"claraverse/internal/database"
	"claraverse/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MaxDaemonsPerUser is the maximum number of concurrent daemons per user
const MaxDaemonsPerUser = 5

// DaemonPool manages the lifecycle of daemon instances
type DaemonPool struct {
	collection *mongo.Collection

	// In-memory tracking of running daemon goroutines
	runners    sync.Map // daemonID (string) → cancel func
	userCounts sync.Map // userID → *int32 (atomic counter)
	mu         sync.Mutex
}

// NewDaemonPool creates a new daemon pool
func NewDaemonPool(mongodb *database.MongoDB) *DaemonPool {
	return &DaemonPool{
		collection: mongodb.Collection(database.CollectionNexusDaemons),
	}
}

// Create inserts a new daemon record
func (p *DaemonPool) Create(ctx context.Context, daemon *models.Daemon) error {
	daemon.CreatedAt = time.Now()
	if daemon.Status == "" {
		daemon.Status = models.DaemonStatusIdle
	}
	if daemon.MaxIterations == 0 {
		daemon.MaxIterations = 25
	}
	if daemon.MaxRetries == 0 {
		daemon.MaxRetries = 3
	}

	result, err := p.collection.InsertOne(ctx, daemon)
	if err != nil {
		return fmt.Errorf("failed to create daemon: %w", err)
	}

	daemon.ID = result.InsertedID.(primitive.ObjectID)
	return nil
}

// GetByID retrieves a daemon by ID, scoped to user
func (p *DaemonPool) GetByID(ctx context.Context, userID string, daemonID primitive.ObjectID) (*models.Daemon, error) {
	var daemon models.Daemon
	err := p.collection.FindOne(ctx, bson.M{
		"_id":    daemonID,
		"userId": userID,
	}).Decode(&daemon)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("daemon not found")
		}
		return nil, fmt.Errorf("failed to get daemon: %w", err)
	}
	return &daemon, nil
}

// GetUserDaemons returns all daemons for a user
func (p *DaemonPool) GetUserDaemons(ctx context.Context, userID string) ([]models.Daemon, error) {
	cursor, err := p.collection.Find(ctx, bson.M{
		"userId": userID,
	}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
	if err != nil {
		return nil, fmt.Errorf("failed to get user daemons: %w", err)
	}
	defer cursor.Close(ctx)

	var daemons []models.Daemon
	if err := cursor.All(ctx, &daemons); err != nil {
		return nil, fmt.Errorf("failed to decode daemons: %w", err)
	}
	return daemons, nil
}

// GetActiveDaemons returns running/executing daemons for a user
func (p *DaemonPool) GetActiveDaemons(ctx context.Context, userID string) ([]models.Daemon, error) {
	cursor, err := p.collection.Find(ctx, bson.M{
		"userId": userID,
		"status": bson.M{"$in": []models.DaemonStatus{
			models.DaemonStatusExecuting,
			models.DaemonStatusWaitingInput,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get active daemons: %w", err)
	}
	defer cursor.Close(ctx)

	var daemons []models.Daemon
	if err := cursor.All(ctx, &daemons); err != nil {
		return nil, fmt.Errorf("failed to decode daemons: %w", err)
	}
	return daemons, nil
}

// GetByTask returns daemons associated with a task
func (p *DaemonPool) GetByTask(ctx context.Context, userID string, taskID primitive.ObjectID) ([]models.Daemon, error) {
	cursor, err := p.collection.Find(ctx, bson.M{
		"userId": userID,
		"taskId": taskID,
	}, options.Find().SetSort(bson.D{{Key: "planIndex", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("failed to get task daemons: %w", err)
	}
	defer cursor.Close(ctx)

	var daemons []models.Daemon
	if err := cursor.All(ctx, &daemons); err != nil {
		return nil, fmt.Errorf("failed to decode daemons: %w", err)
	}
	return daemons, nil
}

// UpdateStatus updates a daemon's status
func (p *DaemonPool) UpdateStatus(ctx context.Context, userID string, daemonID primitive.ObjectID, status models.DaemonStatus, action string, progress float64) error {
	update := bson.M{
		"$set": bson.M{
			"status":        status,
			"currentAction": action,
			"progress":      progress,
		},
	}

	switch status {
	case models.DaemonStatusExecuting:
		now := time.Now()
		update["$set"].(bson.M)["startedAt"] = now
	case models.DaemonStatusCompleted, models.DaemonStatusFailed:
		now := time.Now()
		update["$set"].(bson.M)["completedAt"] = now
	}

	result, err := p.collection.UpdateOne(ctx, bson.M{
		"_id":    daemonID,
		"userId": userID,
	}, update)
	if err != nil {
		return fmt.Errorf("failed to update daemon status: %w", err)
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("daemon not found")
	}
	return nil
}

// AppendMessage adds a message to the daemon's conversation log
func (p *DaemonPool) AppendMessage(ctx context.Context, userID string, daemonID primitive.ObjectID, msg models.DaemonMessage) error {
	msg.Timestamp = time.Now()
	_, err := p.collection.UpdateOne(ctx, bson.M{
		"_id":    daemonID,
		"userId": userID,
	}, bson.M{
		"$push": bson.M{"messages": msg},
		"$inc":  bson.M{"iterations": 1},
	})
	if err != nil {
		return fmt.Errorf("failed to append daemon message: %w", err)
	}
	return nil
}

// AddWorkingMemory adds an entry to the daemon's working memory
func (p *DaemonPool) AddWorkingMemory(ctx context.Context, userID string, daemonID primitive.ObjectID, entry models.DaemonMemoryEntry) error {
	entry.Timestamp = time.Now()
	_, err := p.collection.UpdateOne(ctx, bson.M{
		"_id":    daemonID,
		"userId": userID,
	}, bson.M{
		"$push": bson.M{"workingMemory": entry},
	})
	if err != nil {
		return fmt.Errorf("failed to add working memory: %w", err)
	}
	return nil
}

// RegisterRunner registers a running daemon's cancel function for lifecycle management
func (p *DaemonPool) RegisterRunner(daemonID string, cancel context.CancelFunc) {
	p.runners.Store(daemonID, cancel)
}

// UnregisterRunner removes a daemon runner from the pool
func (p *DaemonPool) UnregisterRunner(daemonID string) {
	p.runners.Delete(daemonID)
}

// Cancel stops a specific daemon by calling its cancel func.
//
// Deprecated: prefer CancelForUser, which verifies ownership before
// touching the runner map. This bare Cancel exists only for internal
// callers that already validated ownership upstream (e.g. orchestrator
// cleanup paths that loaded the daemon themselves).
func (p *DaemonPool) Cancel(daemonID string) error {
	val, ok := p.runners.Load(daemonID)
	if !ok {
		return fmt.Errorf("daemon runner not found: %s", daemonID)
	}
	cancel := val.(context.CancelFunc)
	cancel()
	p.runners.Delete(daemonID)
	return nil
}

// CancelForUser cancels a daemon only if it belongs to the requesting
// user. Required ownership check for any external/HTTP/WebSocket caller —
// without it, anyone with a daemon ObjectID could cancel another user's
// daemon. ObjectIDs are random enough that this is hard to exploit
// blindly, but the architectural gap should still be closed.
//
// Returns an error if the daemon doesn't exist OR belongs to a
// different user; both surface the same "not found" to the caller so
// daemon existence doesn't leak across users.
func (p *DaemonPool) CancelForUser(ctx context.Context, userID, daemonIDHex string) error {
	oid, err := primitive.ObjectIDFromHex(daemonIDHex)
	if err != nil {
		return fmt.Errorf("daemon not found")
	}
	d, err := p.GetByID(ctx, userID, oid)
	if err != nil || d == nil {
		return fmt.Errorf("daemon not found")
	}
	return p.Cancel(daemonIDHex)
}

// CancelAllForUser cancels all running daemons for a user
func (p *DaemonPool) CancelAllForUser(ctx context.Context, userID string) error {
	daemons, err := p.GetActiveDaemons(ctx, userID)
	if err != nil {
		return err
	}
	for _, d := range daemons {
		_ = p.Cancel(d.ID.Hex())
	}
	return nil
}

// CleanupStaleDaemons marks all executing/idle daemons as failed on startup
// (these are zombies from a previous crash/restart)
func (p *DaemonPool) CleanupStaleDaemons(ctx context.Context) (int64, error) {
	result, err := p.collection.UpdateMany(ctx, bson.M{
		"status": bson.M{"$in": []models.DaemonStatus{
			models.DaemonStatusExecuting,
			models.DaemonStatusWaitingInput,
			models.DaemonStatusIdle,
		}},
	}, bson.M{
		"$set": bson.M{
			"status":        models.DaemonStatusFailed,
			"currentAction": "server restarted",
			"completedAt":   time.Now(),
		},
	})
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup stale daemons: %w", err)
	}
	return result.ModifiedCount, nil
}

// ActiveCount returns the number of active daemons for a user
func (p *DaemonPool) ActiveCount(ctx context.Context, userID string) (int, error) {
	count, err := p.collection.CountDocuments(ctx, bson.M{
		"userId": userID,
		"status": bson.M{"$in": []models.DaemonStatus{
			models.DaemonStatusExecuting,
			models.DaemonStatusWaitingInput,
		}},
	})
	if err != nil {
		return 0, fmt.Errorf("failed to count active daemons: %w", err)
	}
	return int(count), nil
}

// CanDeploy checks if the user has capacity for another daemon
func (p *DaemonPool) CanDeploy(ctx context.Context, userID string) (bool, error) {
	count, err := p.ActiveCount(ctx, userID)
	if err != nil {
		return false, err
	}
	return count < MaxDaemonsPerUser, nil
}
