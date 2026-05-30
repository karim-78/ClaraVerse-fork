// seed_orphan inserts a synthetic orphaned Nexus orchestration into Mongo to
// e2e-test the durability + resume path:
//
//   1. Picks any existing user.
//   2. Creates a parent multi_daemon task + 2 sub-tasks + 2 daemon docs.
//   3. Marks daemon[0] completed, daemon[1] still pending.
//   4. Inserts a nexus_orchestration_state with daemon[0] already checkpointed
//      and last_heartbeat_at = 5 minutes ago (well past the 90s orphan threshold).
//
// After running, restart the backend container and watch logs:
//   - [NEXUS-RESUME] Found 1 orphaned Nexus orchestration(s)
//   - [NEXUS-DURABILITY] Resuming task XXX with 1 daemon(s) already done
//   - daemon[1] gets launched, daemon[0] is skipped.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017/claraverse"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer client.Disconnect(context.Background())

	db := client.Database("claraverse")

	// 1. Find any existing user — durability resume needs a real user ID since
	//    the orchestrator looks up sessions/projects by user.
	var userDoc struct {
		ID primitive.ObjectID `bson:"_id"`
	}
	if err := db.Collection("users").FindOne(ctx, bson.M{}).Decode(&userDoc); err != nil {
		log.Fatalf("no user exists yet — sign in via the UI once then re-run: %v", err)
	}
	userID := userDoc.ID.Hex()
	fmt.Printf("→ using user_id=%s\n", userID)

	// 2. Pick or create a session for this user.
	var sessionID primitive.ObjectID
	var sessionDoc struct {
		ID primitive.ObjectID `bson:"_id"`
	}
	if err := db.Collection("nexus_sessions").FindOne(ctx, bson.M{"userId": userID}).Decode(&sessionDoc); err == nil {
		sessionID = sessionDoc.ID
	} else {
		sessionID = primitive.NewObjectID()
		_, _ = db.Collection("nexus_sessions").InsertOne(ctx, bson.M{
			"_id":       sessionID,
			"userId":    userID,
			"createdAt": time.Now().UTC(),
			"updatedAt": time.Now().UTC(),
		})
	}
	fmt.Printf("→ using session_id=%s\n", sessionID.Hex())

	// Clean up any prior e2e seed so each run starts fresh.
	cleanup, _ := db.Collection("nexus_orchestration_state").DeleteMany(ctx,
		bson.M{"original_message": bson.M{"$regex": `^\[E2E test\]`}})
	if cleanup != nil && cleanup.DeletedCount > 0 {
		fmt.Printf("→ cleaned up %d prior e2e state doc(s)\n", cleanup.DeletedCount)
	}

	// 3. Create parent multi_daemon task. NexusTask uses camelCase BSON.
	parentID := primitive.NewObjectID()
	_, err = db.Collection("nexus_tasks").InsertOne(ctx, bson.M{
		"_id":       parentID,
		"sessionId": sessionID,
		"userId":    userID,
		"prompt":    "[E2E test] research a topic then summarize it",
		"goal":      "[E2E test] research a topic then summarize it",
		"mode":      "multi_daemon",
		"status":    "executing",
		"createdAt": time.Now().UTC(),
	})
	if err != nil {
		log.Fatalf("insert parent task: %v", err)
	}
	fmt.Printf("→ parent_task_id=%s\n", parentID.Hex())

	// 4. Create sub-tasks + daemon docs for two plans:
	//    plan 0 = researcher (already completed)
	//    plan 1 = summarizer (depends on researcher, still pending)
	type planMeta struct {
		idx       int
		role      string
		label     string
		dependsOn []int
		completed bool
	}
	plans := []planMeta{
		{idx: 0, role: "researcher", label: "Researcher Daemon", dependsOn: nil, completed: true},
		{idx: 1, role: "summarizer", label: "Summarizer Daemon", dependsOn: []int{0}, completed: false},
	}

	daemonIDs := make(map[string]primitive.ObjectID, len(plans))
	bsonPlans := bson.A{}
	completedRecords := bson.M{}

	for _, p := range plans {
		subTaskID := primitive.NewObjectID()
		_, _ = db.Collection("nexus_tasks").InsertOne(ctx, bson.M{
			"_id":          subTaskID,
			"sessionId":    sessionID,
			"userId":       userID,
			"parentTaskId": parentID,
			"prompt":       fmt.Sprintf("[%s] subtask", p.role),
			"goal":         fmt.Sprintf("[%s] subtask", p.role),
			"mode":         "daemon",
			"status":       "executing",
			"createdAt":    time.Now().UTC(),
		})

		daemonID := primitive.NewObjectID()
		daemonStatus := "idle"
		if p.completed {
			daemonStatus = "completed"
		}
		// NOTE: Daemon model uses camelCase BSON tags (userId, sessionId, taskId,
		// roleLabel, assignedTools, planIndex, dependsOn, currentAction,
		// maxIterations, maxRetries, modelId, createdAt) — see internal/models/nexus_daemon.go.
		// daemonPool.GetByID filters by `userId` + `_id`, so these names must match exactly.
		_, _ = db.Collection("nexus_daemons").InsertOne(ctx, bson.M{
			"_id":           daemonID,
			"sessionId":     sessionID,
			"userId":        userID,
			"taskId":        subTaskID,
			"role":          p.role,
			"roleLabel":     p.label,
			"persona":       "test daemon",
			"assignedTools": bson.A{},
			"planIndex":     p.idx,
			"dependsOn":     p.dependsOn,
			"status":        daemonStatus,
			"currentAction": "e2e test stub",
			"progress":      0.0,
			"iterations":    0,
			"maxIterations": 25,
			"retryCount":    0,
			"maxRetries":    3,
			"modelId":       "test-model",
			"createdAt":     time.Now().UTC(),
		})
		daemonIDs[fmt.Sprintf("%d", p.idx)] = daemonID

		planBson := bson.M{
			"index":         p.idx,
			"role":          p.role,
			"role_label":    p.label,
			"persona":       "test daemon",
			"task_summary":  fmt.Sprintf("[%s] subtask", p.role),
			"tools_needed":  bson.A{},
			"depends_on":    bson.A{},
			"template_slug": "",
		}
		if p.dependsOn != nil {
			arr := bson.A{}
			for _, d := range p.dependsOn {
				arr = append(arr, d)
			}
			planBson["depends_on"] = arr
		}
		bsonPlans = append(bsonPlans, planBson)

		if p.completed {
			completedRecords[fmt.Sprintf("%d", p.idx)] = bson.M{
				"index":        p.idx,
				"daemon_id":    daemonID.Hex(),
				"role":         p.role,
				"role_label":   p.label,
				"summary":      "[e2e checkpoint] researcher already finished before the crash",
				"completed_at": time.Now().UTC().Add(-2 * time.Minute),
			}
		}

		fmt.Printf("   plan[%d] daemon=%s status=%s completed=%v\n",
			p.idx, daemonID.Hex(), daemonStatus, p.completed)
	}

	// 5. Insert the orphaned orchestration state.
	staleHeartbeat := time.Now().UTC().Add(-5 * time.Minute)
	stateDoc := bson.M{
		"session_id":          sessionID,
		"user_id":             userID,
		"parent_task_id":      parentID,
		"model_id":            "test-model",
		"original_message":    "[E2E test] research a topic then summarize it",
		"project_instruction": "",
		"is_routine":          false,
		"plans":               bsonPlans,
		"daemon_ids":          daemonIDs,
		"skill_ids":           bson.A{},
		"status":              "running",
		"started_at":          time.Now().UTC().Add(-10 * time.Minute),
		"last_heartbeat_at":   staleHeartbeat,
		"completed_daemons":   completedRecords,
	}
	res, err := db.Collection("nexus_orchestration_state").InsertOne(ctx, stateDoc)
	if err != nil {
		log.Fatalf("insert state: %v", err)
	}
	fmt.Printf("→ state doc inserted: _id=%v\n", res.InsertedID)
	fmt.Printf("→ last_heartbeat_at = %s (%.0fs ago — past the 90s orphan threshold)\n",
		staleHeartbeat.Format(time.RFC3339), time.Since(staleHeartbeat).Seconds())
	fmt.Println()
	fmt.Println("✅ synthetic orphan seeded. Now restart the backend:")
	fmt.Println("   docker compose restart backend")
	fmt.Println("   docker compose logs -f backend | grep -E 'NEXUS-RESUME|NEXUS-DURABILITY'")
}
