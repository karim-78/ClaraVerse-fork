package services

import (
	"context"
	"fmt"
	"time"

	"claraverse/internal/database"
	"claraverse/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// NexusTaskStore handles MongoDB CRUD for Nexus tasks
type NexusTaskStore struct {
	collection *mongo.Collection
}

// NewNexusTaskStore creates a new task store
func NewNexusTaskStore(mongodb *database.MongoDB) *NexusTaskStore {
	return &NexusTaskStore{
		collection: mongodb.Collection(database.CollectionNexusTasks),
	}
}

// CleanupStaleTasks marks tasks stuck in non-terminal states from a prior
// backend run as failed. Without this, tasks that were executing when the
// backend died stay in "executing" or "pending" forever, polluting the
// kanban and confusing users about "queued tasks never starting".
//
// Threshold: any task with status in {pending, queued, executing, waiting_input}
// that was last updated more than `staleAfter` ago is considered orphaned.
// We use updatedAt (heartbeat-ish) rather than createdAt so a long-running
// but still-alive task that updated recently isn't swept.
//
// Returns the number of tasks updated.
func (s *NexusTaskStore) CleanupStaleTasks(ctx context.Context, staleAfter time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-staleAfter)
	filter := bson.M{
		"status": bson.M{"$in": []string{
			string(models.NexusTaskStatusPending),
			string(models.NexusTaskStatusExecuting),
			string(models.NexusTaskStatusWaitingInput),
		}},
		// Either updatedAt is missing (very old record) or older than cutoff.
		"$or": []bson.M{
			{"updatedAt": bson.M{"$lt": cutoff}},
			{"updatedAt": bson.M{"$exists": false}},
		},
	}
	update := bson.M{"$set": bson.M{
		"status":    string(models.NexusTaskStatusFailed),
		"error":     "backend restarted before this task completed",
		"updatedAt": time.Now().UTC(),
	}}
	res, err := s.collection.UpdateMany(ctx, filter, update)
	if err != nil {
		return 0, fmt.Errorf("cleanup stale tasks: %w", err)
	}
	return res.ModifiedCount, nil
}

// Create inserts a new task
func (s *NexusTaskStore) Create(ctx context.Context, task *models.NexusTask) error {
	now := time.Now()
	task.CreatedAt = now
	task.UpdatedAt = now
	if task.Status == "" {
		task.Status = models.NexusTaskStatusPending
	}

	result, err := s.collection.InsertOne(ctx, task)
	if err != nil {
		return fmt.Errorf("failed to create nexus task: %w", err)
	}

	task.ID = result.InsertedID.(primitive.ObjectID)
	return nil
}

// GetByID retrieves a task by ID, scoped to user
func (s *NexusTaskStore) GetByID(ctx context.Context, userID string, taskID primitive.ObjectID) (*models.NexusTask, error) {
	var task models.NexusTask
	err := s.collection.FindOne(ctx, bson.M{
		"_id":    taskID,
		"userId": userID,
	}).Decode(&task)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("task not found")
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	return &task, nil
}

// GetByIDs retrieves multiple tasks by their IDs, scoped to user
func (s *NexusTaskStore) GetByIDs(ctx context.Context, userID string, taskIDs []primitive.ObjectID) ([]models.NexusTask, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	cursor, err := s.collection.Find(ctx, bson.M{
		"_id":    bson.M{"$in": taskIDs},
		"userId": userID,
	}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
	if err != nil {
		return nil, fmt.Errorf("failed to get tasks by IDs: %w", err)
	}
	defer cursor.Close(ctx)

	var tasks []models.NexusTask
	if err := cursor.All(ctx, &tasks); err != nil {
		return nil, fmt.Errorf("failed to decode tasks: %w", err)
	}
	return tasks, nil
}

// TaskFilters defines filter options for listing tasks
type TaskFilters struct {
	Status       models.NexusTaskStatus
	ProjectID    *string // nil = all, "" (empty) = inbox only (no project), "hex" = specific project
	Limit        int64
	Offset       int64
	TopLevelOnly bool // Exclude sub-tasks (tasks with parentTaskId)
}

// List returns tasks for a user with optional filters
func (s *NexusTaskStore) List(ctx context.Context, userID string, filters TaskFilters) ([]models.NexusTask, error) {
	filter := bson.M{"userId": userID}
	if filters.Status != "" {
		filter["status"] = filters.Status
	}
	if filters.TopLevelOnly {
		filter["parentTaskId"] = bson.M{"$exists": false}
	}
	if filters.ProjectID != nil {
		if *filters.ProjectID == "" {
			filter["projectId"] = bson.M{"$exists": false}
		} else {
			oid, err := primitive.ObjectIDFromHex(*filters.ProjectID)
			if err == nil {
				filter["projectId"] = oid
			}
		}
	}

	limit := int64(20)
	if filters.Limit > 0 && filters.Limit <= 100 {
		limit = filters.Limit
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}}).
		SetLimit(limit)
	if filters.Offset > 0 {
		opts.SetSkip(filters.Offset)
	}

	cursor, err := s.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}
	defer cursor.Close(ctx)

	var tasks []models.NexusTask
	if err := cursor.All(ctx, &tasks); err != nil {
		return nil, fmt.Errorf("failed to decode tasks: %w", err)
	}
	return tasks, nil
}

// UpdateStatus updates a task's status and related fields
func (s *NexusTaskStore) UpdateStatus(ctx context.Context, userID string, taskID primitive.ObjectID, status models.NexusTaskStatus) error {
	update := bson.M{
		"$set": bson.M{
			"status":    status,
			"updatedAt": time.Now(),
		},
	}

	switch status {
	case models.NexusTaskStatusExecuting:
		now := time.Now()
		update["$set"].(bson.M)["startedAt"] = now
	case models.NexusTaskStatusCompleted, models.NexusTaskStatusFailed, models.NexusTaskStatusCancelled:
		now := time.Now()
		update["$set"].(bson.M)["completedAt"] = now
	}

	result, err := s.collection.UpdateOne(ctx, bson.M{
		"_id":    taskID,
		"userId": userID,
	}, update)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("task not found")
	}
	return nil
}

// UpdateModeAndStatus updates mode, status, and optionally goal of a task (used when classification completes)
func (s *NexusTaskStore) UpdateModeAndStatus(ctx context.Context, userID string, taskID primitive.ObjectID, mode string, status models.NexusTaskStatus, goal string) error {
	setFields := bson.M{
		"mode":      mode,
		"status":    status,
		"updatedAt": time.Now(),
	}
	if goal != "" {
		setFields["goal"] = goal
	}
	if status == models.NexusTaskStatusExecuting {
		setFields["startedAt"] = time.Now()
	}
	_, err := s.collection.UpdateOne(ctx, bson.M{
		"_id":    taskID,
		"userId": userID,
	}, bson.M{"$set": setFields})
	if err != nil {
		return fmt.Errorf("failed to update task mode/status: %w", err)
	}
	return nil
}

// SetResult sets the task result and marks it completed
func (s *NexusTaskStore) SetResult(ctx context.Context, userID string, taskID primitive.ObjectID, result *models.NexusTaskResult) error {
	now := time.Now()
	_, err := s.collection.UpdateOne(ctx, bson.M{
		"_id":    taskID,
		"userId": userID,
	}, bson.M{
		"$set": bson.M{
			"status":      models.NexusTaskStatusCompleted,
			"result":      result,
			"completedAt": now,
			"updatedAt":   now,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to set task result: %w", err)
	}
	return nil
}

// SetError sets the task error and marks it failed
func (s *NexusTaskStore) SetError(ctx context.Context, userID string, taskID primitive.ObjectID, errMsg string) error {
	now := time.Now()
	_, err := s.collection.UpdateOne(ctx, bson.M{
		"_id":    taskID,
		"userId": userID,
	}, bson.M{
		"$set": bson.M{
			"status":      models.NexusTaskStatusFailed,
			"error":       errMsg,
			"completedAt": now,
			"updatedAt":   now,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to set task error: %w", err)
	}
	return nil
}

// SetDaemonID associates a daemon with a task
func (s *NexusTaskStore) SetDaemonID(ctx context.Context, userID string, taskID primitive.ObjectID, daemonID primitive.ObjectID) error {
	_, err := s.collection.UpdateOne(ctx, bson.M{
		"_id":    taskID,
		"userId": userID,
	}, bson.M{
		"$set": bson.M{
			"daemonId":  daemonID,
			"updatedAt": time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to set daemon ID: %w", err)
	}
	return nil
}

// AddSubTaskID adds a sub-task reference to a parent task
func (s *NexusTaskStore) AddSubTaskID(ctx context.Context, userID string, parentTaskID, subTaskID primitive.ObjectID) error {
	_, err := s.collection.UpdateOne(ctx, bson.M{
		"_id":    parentTaskID,
		"userId": userID,
	}, bson.M{
		"$push": bson.M{"subTaskIds": subTaskID},
		"$set":  bson.M{"updatedAt": time.Now()},
	})
	if err != nil {
		return fmt.Errorf("failed to add sub-task ID: %w", err)
	}
	return nil
}

// SetProjectID assigns or removes a task from a project
func (s *NexusTaskStore) SetProjectID(ctx context.Context, userID string, taskID primitive.ObjectID, projectID *primitive.ObjectID) error {
	var update bson.M
	if projectID != nil {
		update = bson.M{"$set": bson.M{"projectId": *projectID, "updatedAt": time.Now()}}
	} else {
		update = bson.M{"$unset": bson.M{"projectId": ""}, "$set": bson.M{"updatedAt": time.Now()}}
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{
		"_id":    taskID,
		"userId": userID,
	}, update)
	if err != nil {
		return fmt.Errorf("failed to set project ID: %w", err)
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("task not found")
	}
	return nil
}

// UnsetProjectForAll removes projectId from all tasks in a given project (used when deleting a project)
func (s *NexusTaskStore) UnsetProjectForAll(ctx context.Context, userID string, projectID primitive.ObjectID) error {
	_, err := s.collection.UpdateMany(ctx, bson.M{
		"userId":    userID,
		"projectId": projectID,
	}, bson.M{
		"$unset": bson.M{"projectId": ""},
		"$set":   bson.M{"updatedAt": time.Now()},
	})
	if err != nil {
		return fmt.Errorf("failed to unset project for tasks: %w", err)
	}
	return nil
}

// ReassignProjectTasks moves all tasks from one project to another
func (s *NexusTaskStore) ReassignProjectTasks(ctx context.Context, userID string, fromProjectID, toProjectID primitive.ObjectID) error {
	_, err := s.collection.UpdateMany(ctx, bson.M{
		"userId":    userID,
		"projectId": fromProjectID,
	}, bson.M{
		"$set": bson.M{"projectId": toProjectID, "updatedAt": time.Now()},
	})
	if err != nil {
		return fmt.Errorf("failed to reassign project tasks: %w", err)
	}
	return nil
}

// UpdateContent updates the prompt/goal of a draft task
func (s *NexusTaskStore) UpdateContent(ctx context.Context, userID string, taskID primitive.ObjectID, prompt, goal string) error {
	setFields := bson.M{"updatedAt": time.Now()}
	if prompt != "" {
		setFields["prompt"] = prompt
	}
	if goal != "" {
		setFields["goal"] = goal
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{
		"_id":    taskID,
		"userId": userID,
		"status": models.NexusTaskStatusDraft,
	}, bson.M{"$set": setFields})
	if err != nil {
		return fmt.Errorf("failed to update task content: %w", err)
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("draft task not found")
	}
	return nil
}

// Delete removes a task by ID (only terminal or draft tasks)
func (s *NexusTaskStore) Delete(ctx context.Context, userID string, taskID primitive.ObjectID) error {
	result, err := s.collection.DeleteOne(ctx, bson.M{
		"_id":    taskID,
		"userId": userID,
		"status": bson.M{"$in": []string{"draft", "completed", "failed", "cancelled"}},
	})
	if err != nil {
		return fmt.Errorf("failed to delete task: %w", err)
	}
	if result.DeletedCount == 0 {
		return fmt.Errorf("task not found or not in terminal state")
	}
	return nil
}

// CountManualRetries counts how many manual retry tasks exist for a given root task ID
func (s *NexusTaskStore) CountManualRetries(ctx context.Context, userID string, rootTaskID primitive.ObjectID) (int, error) {
	count, err := s.collection.CountDocuments(ctx, bson.M{
		"userId":        userID,
		"retryOfTaskId": rootTaskID,
		"source":        "manual_retry",
	})
	if err != nil {
		return 0, fmt.Errorf("failed to count retries: %w", err)
	}
	return int(count), nil
}

// GetRootTaskID returns the root of a retry chain. If the task has no RetryOfTaskID, it IS the root.
func (s *NexusTaskStore) GetRootTaskID(ctx context.Context, userID string, taskID primitive.ObjectID) (primitive.ObjectID, error) {
	task, err := s.GetByID(ctx, userID, taskID)
	if err != nil {
		return taskID, err
	}
	if task.RetryOfTaskID != nil {
		return *task.RetryOfTaskID, nil
	}
	return task.ID, nil
}

// GetRecentBySession returns the most recent tasks for a session
func (s *NexusTaskStore) GetRecentBySession(ctx context.Context, userID string, sessionID primitive.ObjectID, limit int64) ([]models.NexusTask, error) {
	if limit <= 0 {
		limit = 20
	}

	cursor, err := s.collection.Find(ctx, bson.M{
		"userId":    userID,
		"sessionId": sessionID,
	}, options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}}).
		SetLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("failed to get recent tasks: %w", err)
	}
	defer cursor.Close(ctx)

	var tasks []models.NexusTask
	if err := cursor.All(ctx, &tasks); err != nil {
		return nil, fmt.Errorf("failed to decode tasks: %w", err)
	}
	return tasks, nil
}

// GetRecentBySessionAndProject returns recent tasks scoped to a specific project.
// When projectID is nil, returns only tasks with no project (inbox).
func (s *NexusTaskStore) GetRecentBySessionAndProject(ctx context.Context, userID string, sessionID primitive.ObjectID, projectID *primitive.ObjectID, limit int64) ([]models.NexusTask, error) {
	if limit <= 0 {
		limit = 20
	}

	filter := bson.M{
		"userId":    userID,
		"sessionId": sessionID,
	}
	if projectID != nil {
		filter["projectId"] = *projectID
	} else {
		filter["projectId"] = bson.M{"$exists": false}
	}

	cursor, err := s.collection.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}}).
		SetLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("failed to get recent tasks by project: %w", err)
	}
	defer cursor.Close(ctx)

	var tasks []models.NexusTask
	if err := cursor.All(ctx, &tasks); err != nil {
		return nil, fmt.Errorf("failed to decode tasks: %w", err)
	}
	return tasks, nil
}

// GetByRoutineID returns tasks created by a specific routine, sorted newest-first.
// Uses a projection to keep responses lean (no full result data/artifacts).
func (s *NexusTaskStore) GetByRoutineID(ctx context.Context, userID string, routineID primitive.ObjectID, limit int64) ([]models.NexusTask, error) {
	if limit <= 0 {
		limit = 20
	}

	cursor, err := s.collection.Find(ctx, bson.M{
		"userId":    userID,
		"routineId": routineID,
	}, options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}}).
		SetLimit(limit).
		SetProjection(bson.M{
			"_id": 1, "status": 1, "mode": 1, "goal": 1,
			"result.summary": 1, "error": 1,
			"createdAt": 1, "completedAt": 1,
		}))
	if err != nil {
		return nil, fmt.Errorf("failed to get tasks by routine: %w", err)
	}
	defer cursor.Close(ctx)

	var tasks []models.NexusTask
	if err := cursor.All(ctx, &tasks); err != nil {
		return nil, fmt.Errorf("failed to decode routine tasks: %w", err)
	}
	return tasks, nil
}
