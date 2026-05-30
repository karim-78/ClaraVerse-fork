// check_orphan inspects the most recent e2e orchestration state doc to
// verify it transitioned out of "running" after resume.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017/claraverse"))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer client.Disconnect(context.Background())

	coll := client.Database("claraverse").Collection("nexus_orchestration_state")
	var doc bson.M
	err = coll.FindOne(ctx,
		bson.M{"original_message": bson.M{"$regex": `^\[E2E test\]`}},
		options.FindOne().SetSort(bson.D{{Key: "started_at", Value: -1}}),
	).Decode(&doc)
	if err != nil {
		log.Fatalf("find: %v", err)
	}

	out := map[string]interface{}{
		"status":              doc["status"],
		"started_at":          doc["started_at"],
		"last_heartbeat_at":   doc["last_heartbeat_at"],
		"completed_at":        doc["completed_at"],
		"completed_daemons":   completedKeys(doc["completed_daemons"]),
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}

func completedKeys(v interface{}) []string {
	m, ok := v.(bson.M)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
