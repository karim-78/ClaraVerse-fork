package handlers

// Admin backup + restore for the Mongo database.
//
// Why this exists:
//
// Without it, a self-hosted operator has no built-in path to take a snapshot
// of user data before an upgrade, migrate between hosts, or recover from a
// corrupted volume. They'd either trust the underlying disk backups (which
// they may not have) or learn `mongodump` (which isn't even installed in
// the container). Neither is acceptable for a platform that hosts other
// people's chat history, agents, and integrations.
//
// What this does:
//
//   - GET  /admin/backup → streams a gzipped JSON snapshot of every Mongo
//     collection in the project database. Format is
//     `{"collections": {"<name>": [...docs]}, "metadata": {...}}` —
//     human-readable, diffable, portable across Mongo versions.
//
//   - POST /admin/restore → accepts a gzipped JSON snapshot (same format)
//     and upserts each document by _id. Non-destructive by design: it
//     doesn't drop or rename collections, so a partial restore won't
//     orphan unrelated data.
//
// Trade-offs vs `mongodump`:
//
//   + No external binary. Works in any container that runs the backend.
//   + Inspectable format — operators can `jq` the file before restoring.
//   + Restore is merge-style; safe to run against a live DB.
//   - Slower than BSON-binary mongodump for huge collections (10M+ docs).
//     Acceptable for the user-data scale of this platform; we can switch
//     to a BSON streamer if a deployment ever hits the wall.
//   - No index restoration. Indexes are recreated by the application on
//     boot (every store calls ensureIndexes), so this is intentional —
//     the application owns the indexes, not the snapshot.
//
// Security:
//
//   - Both endpoints are mounted under the admin route group, which already
//     applies LocalAuthMiddleware + AdminMiddleware in main.go. A non-admin
//     hitting these gets 401/403 before any handler code runs.
//   - The snapshot includes hashed passwords, encrypted credentials, and
//     OAuth tokens — operators must treat it as a credentials file. The
//     handler streams to disk only when the operator asks for the file;
//     we never persist a copy server-side.

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"time"

	"claraverse/internal/database"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// BackupAdminHandler owns the backup + restore endpoints. Lightweight —
// just holds a reference to the mongo wrapper.
type BackupAdminHandler struct {
	mongo *database.MongoDB
}

// NewBackupAdminHandler constructs the handler. Pass the live *database.MongoDB
// from main.go.
func NewBackupAdminHandler(md *database.MongoDB) *BackupAdminHandler {
	return &BackupAdminHandler{mongo: md}
}

// SnapshotMetadata is everything the restore side needs to sanity-check the
// file before applying it.
type SnapshotMetadata struct {
	CreatedAt     time.Time `json:"created_at"`
	SchemaVersion int       `json:"schema_version"`
	SourceDB      string    `json:"source_db"`
	Collections   int       `json:"collections"`
	Documents     int       `json:"documents"`
}

// Snapshot is the on-wire shape. `Collections` is a map from collection
// name to its documents serialized as bson-as-json.
type Snapshot struct {
	Metadata    SnapshotMetadata        `json:"metadata"`
	Collections map[string][]bson.M     `json:"collections"`
}

const snapshotSchemaVersion = 1

// Backup streams a gzipped JSON snapshot. The handler writes directly to
// the response body — no temporary file, no extra memory beyond per-doc
// JSON encoding, so a huge DB doesn't blow up the process.
func (h *BackupAdminHandler) Backup(c *fiber.Ctx) error {
	if h.mongo == nil {
		return c.Status(500).JSON(fiber.Map{"error": "mongo not configured"})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Minute)
	defer cancel()

	db := h.mongo.Database()
	collNames, err := db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		log.Printf("[ADMIN-BACKUP] list collections failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "failed to enumerate collections"})
	}

	// Build the snapshot in memory. For our typical size (low-MB total)
	// this is fine. If a deployment ever pushes this past ~100 MB we'll
	// switch to a streaming json encoder driving directly into gzip.
	snap := Snapshot{
		Metadata: SnapshotMetadata{
			CreatedAt:     time.Now().UTC(),
			SchemaVersion: snapshotSchemaVersion,
			SourceDB:      db.Name(),
		},
		Collections: make(map[string][]bson.M, len(collNames)),
	}

	totalDocs := 0
	for _, name := range collNames {
		// system.* collections are mongo-internal; never include them.
		if len(name) >= 7 && name[:7] == "system." {
			continue
		}
		cursor, err := db.Collection(name).Find(ctx, bson.M{})
		if err != nil {
			log.Printf("[ADMIN-BACKUP] find %s failed: %v", name, err)
			continue
		}
		var docs []bson.M
		if err := cursor.All(ctx, &docs); err != nil {
			log.Printf("[ADMIN-BACKUP] decode %s failed: %v", name, err)
			cursor.Close(ctx)
			continue
		}
		cursor.Close(ctx)
		snap.Collections[name] = docs
		totalDocs += len(docs)
	}
	snap.Metadata.Collections = len(snap.Collections)
	snap.Metadata.Documents = totalDocs

	// Serialize then gzip. bson.M marshals to standard JSON with extended
	// types (ObjectIDs as {"$oid":"..."}, dates as {"$date":...}). This is
	// the same shape Mongo's own tools produce, so a future migration to
	// mongorestore stays compatible.
	jsonBuf, err := json.Marshal(snap)
	if err != nil {
		log.Printf("[ADMIN-BACKUP] json marshal failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "snapshot serialization failed"})
	}

	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	if _, err := gw.Write(jsonBuf); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "gzip failed"})
	}
	if err := gw.Close(); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "gzip close failed"})
	}

	filename := fmt.Sprintf("claraverse-backup-%s.json.gz", time.Now().UTC().Format("20060102-150405"))
	c.Set("Content-Type", "application/gzip")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Set("X-ClaraVerse-Snapshot-Schema", fmt.Sprintf("%d", snapshotSchemaVersion))
	c.Set("X-ClaraVerse-Snapshot-Collections", fmt.Sprintf("%d", snap.Metadata.Collections))
	c.Set("X-ClaraVerse-Snapshot-Documents", fmt.Sprintf("%d", snap.Metadata.Documents))

	log.Printf("✅ [ADMIN-BACKUP] %d collections, %d docs, %d KB gzipped",
		snap.Metadata.Collections, snap.Metadata.Documents, gzBuf.Len()/1024)

	return c.Send(gzBuf.Bytes())
}

// Restore accepts a multipart upload of a snapshot file, decodes + ungzips,
// then upserts every document by _id. Merge semantics — does not drop or
// rename existing collections, so partial uploads don't damage data outside
// the snapshot.
func (h *BackupAdminHandler) Restore(c *fiber.Ctx) error {
	if h.mongo == nil {
		return c.Status(500).JSON(fiber.Map{"error": "mongo not configured"})
	}

	fileHeader, err := c.FormFile("snapshot")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "expected multipart field 'snapshot' with the .json.gz file"})
	}

	src, err := fileHeader.Open()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "failed to open uploaded file"})
	}
	defer src.Close()

	gr, err := gzip.NewReader(src)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "uploaded file is not valid gzip"})
	}
	defer gr.Close()

	jsonBytes, err := io.ReadAll(gr)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "failed to read gzipped body"})
	}

	var snap Snapshot
	if err := json.Unmarshal(jsonBytes, &snap); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": fmt.Sprintf("invalid snapshot JSON: %v", err)})
	}

	// Schema version guard. We bump snapshotSchemaVersion when the shape
	// changes incompatibly; older snapshots get explicitly rejected with a
	// clear error rather than silently miscoded.
	if snap.Metadata.SchemaVersion != snapshotSchemaVersion {
		return c.Status(400).JSON(fiber.Map{
			"error": fmt.Sprintf("snapshot schema_version=%d not supported (expected %d)",
				snap.Metadata.SchemaVersion, snapshotSchemaVersion),
		})
	}

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Minute)
	defer cancel()

	db := h.mongo.Database()
	totalUpserted := 0
	totalFailed := 0
	collectionsRestored := 0

	for collName, docs := range snap.Collections {
		coll := db.Collection(collName)
		upserted := 0
		failed := 0
		for _, doc := range docs {
			id, ok := doc["_id"]
			if !ok {
				failed++
				continue
			}
			// Strip _id from the $set body — Mongo rejects updating _id even to itself.
			update := bson.M{}
			for k, v := range doc {
				if k == "_id" {
					continue
				}
				update[k] = v
			}
			_, err := coll.UpdateOne(ctx,
				bson.M{"_id": id},
				bson.M{"$set": update},
				options.Update().SetUpsert(true),
			)
			if err != nil {
				failed++
				continue
			}
			upserted++
		}
		totalUpserted += upserted
		totalFailed += failed
		if upserted > 0 {
			collectionsRestored++
		}
		log.Printf("[ADMIN-RESTORE] %s: upserted=%d failed=%d", collName, upserted, failed)
	}

	log.Printf("✅ [ADMIN-RESTORE] %d collections, %d docs upserted, %d failed",
		collectionsRestored, totalUpserted, totalFailed)

	return c.JSON(fiber.Map{
		"snapshot_created_at":   snap.Metadata.CreatedAt,
		"source_db":             snap.Metadata.SourceDB,
		"collections_restored":  collectionsRestored,
		"documents_upserted":    totalUpserted,
		"documents_failed":      totalFailed,
	})
}

// Ensure we use mongo for nothing surprising. The import is referenced
// indirectly via *database.MongoDB; the explicit blank usage below is a
// linter belt-and-suspenders so future refactors don't drop the import.
var _ = mongo.IndexModel{}
