//go:build integration

// Shared test helpers for service-layer integration tests.
//
// Build tag `integration` keeps these out of the default `go test` runner
// (which is a unit-only run). Trigger explicitly:
//
//   cd backend && go test -tags=integration -run TestNexus ./internal/services/...
//
// The helpers connect to a real Mongo at MONGODB_URI (default
// mongodb://localhost:27017) and create a fresh, throw-away database
// per test run so parallel runs don't collide and no state leaks between
// tests. The database is dropped on cleanup.
//
// Why integration over unit: durability + resume semantics are mostly
// about Mongo behaviour (TTL, upsert, index uniqueness, query ordering).
// A mock store would let you write green tests for a broken design;
// hitting real Mongo proves the contract.
package services

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"claraverse/internal/database"
)

// mongoBaseURI returns the test Mongo host (no database path). Override
// with MONGODB_URI to point the suite at a different cluster.
func mongoBaseURI() string {
	if v := os.Getenv("MONGODB_URI"); v != "" {
		// Strip any /database suffix so we can append our own.
		if i := strings.LastIndex(v, "/"); i > strings.Index(v, "://")+2 {
			v = v[:i]
		}
		return v
	}
	return "mongodb://localhost:27017"
}

// newTestMongo connects to Mongo via the public NewMongoDB constructor,
// pointed at a freshly-named test database. Skips the test entirely if
// Mongo isn't reachable — keeps CI honest without forcing an infra
// dependency.
//
// The returned cleanup function drops the test database and disconnects.
// Tests MUST defer it.
func newTestMongo(t *testing.T) (*database.MongoDB, func()) {
	t.Helper()

	dbName := fmt.Sprintf("claraverse_itest_%d_%d", time.Now().UnixNano(), os.Getpid())
	uri := mongoBaseURI() + "/" + dbName

	md, err := database.NewMongoDB(uri)
	if err != nil {
		t.Skipf("mongo unreachable at %s: %v", uri, err)
	}

	cleanup := func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer dropCancel()
		// Drop the test database. We use the underlying *mongo.Database
		// from the public accessor.
		_ = md.Database().Drop(dropCtx)
		_ = md.Close(context.Background())
	}
	return md, cleanup
}

// mustOK fails the test fast on a non-nil error. Reads better than
// `if err != nil { t.Fatal(...) }` everywhere.
func mustOK(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}
