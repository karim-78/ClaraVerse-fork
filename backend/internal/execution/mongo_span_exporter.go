package execution

// ============================================================================
// MongoSpanExporter — a sdktrace.SpanExporter that persists spans to Mongo
// so the admin UI has a built-in trace viewer (no external Tempo / Jaeger).
//
// Spans land in collection `workflow_traces`, one document per span. We
// query by trace_id (cheap, single index hit) to render the waterfall view.
// Root spans (those with no parent_span_id) drive the "recent traces" list.
//
// Storage characteristics:
//
//   - One document per span — easy to index, easy to query, easy to TTL.
//     A 50-block workflow produces ~51 docs. Cheap.
//
//   - Indexes: trace_id (waterfall fetch), root listing (parent_span_id="")
//     by start_time desc, TTL 14 days on start_time.
//
//   - Composes with other exporters. The tracing bootstrap registers both
//     this and the stdout/OTLP exporter, so logs + admin UI + external
//     observability stack all see the same data.
//
// Failure mode is "drop and log" — if Mongo is down we don't block the
// request, we just log and skip. Tracing must never break the application.

import (
	"claraverse/internal/database"
	"context"
	"log"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// MongoSpanDoc is the storage shape. Designed so the admin list can read
// only what it needs without decoding the full attribute map.
type MongoSpanDoc struct {
	TraceID      string                 `bson:"trace_id" json:"trace_id"`
	SpanID       string                 `bson:"span_id" json:"span_id"`
	ParentSpanID string                 `bson:"parent_span_id" json:"parent_span_id"`
	Name         string                 `bson:"name" json:"name"`
	Service      string                 `bson:"service" json:"service"`
	Kind         string                 `bson:"kind" json:"kind"`
	StartTime    time.Time              `bson:"start_time" json:"start_time"`
	EndTime      time.Time              `bson:"end_time" json:"end_time"`
	DurationMs   int64                  `bson:"duration_ms" json:"duration_ms"`
	StatusCode   string                 `bson:"status_code" json:"status_code"`
	StatusDesc   string                 `bson:"status_desc,omitempty" json:"status_desc,omitempty"`
	Attributes   map[string]interface{} `bson:"attributes,omitempty" json:"attributes,omitempty"`
}

// MongoSpanExporter implements go.opentelemetry.io/otel/sdk/trace.SpanExporter.
type MongoSpanExporter struct {
	coll   *mongo.Collection
	mu     sync.Mutex
	closed bool
}

// NewMongoSpanExporter wires the exporter to a Mongo collection. Indexes
// are created here (idempotent — re-running on every boot is harmless).
func NewMongoSpanExporter(db *database.MongoDB) (*MongoSpanExporter, error) {
	coll := db.Collection("workflow_traces")
	e := &MongoSpanExporter{coll: coll}
	if err := e.ensureIndexes(context.Background()); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *MongoSpanExporter) ensureIndexes(ctx context.Context) error {
	fourteenDays := int32(14 * 24 * 60 * 60)
	idx := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "trace_id", Value: 1}},
			Options: options.Index().SetName("trace_id_idx"),
		},
		{
			// Listing: roots (parent_span_id="") ordered by most recent.
			Keys:    bson.D{{Key: "parent_span_id", Value: 1}, {Key: "start_time", Value: -1}},
			Options: options.Index().SetName("recent_roots_idx"),
		},
		{
			// TTL — auto-prune old traces after 14 days.
			Keys:    bson.D{{Key: "start_time", Value: 1}},
			Options: options.Index().SetName("trace_ttl").SetExpireAfterSeconds(fourteenDays),
		},
	}
	_, err := e.coll.Indexes().CreateMany(ctx, idx)
	return err
}

// ExportSpans implements sdktrace.SpanExporter. The OTel SDK calls this on
// each batch flush (every ~5s in our config). We do a bulk insert; failure
// is logged but never propagated — observability must never break the app.
func (e *MongoSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return nil
	}
	if len(spans) == 0 {
		return nil
	}

	docs := make([]interface{}, 0, len(spans))
	for _, s := range spans {
		sc := s.SpanContext()
		ps := s.Parent()

		serviceName := ""
		for _, attr := range s.Resource().Attributes() {
			if string(attr.Key) == "service.name" {
				serviceName = attr.Value.AsString()
				break
			}
		}

		attrs := map[string]interface{}{}
		for _, attr := range s.Attributes() {
			attrs[string(attr.Key)] = attr.Value.AsInterface()
		}

		duration := s.EndTime().Sub(s.StartTime())
		parentID := ""
		if ps.HasSpanID() {
			parentID = ps.SpanID().String()
		}

		docs = append(docs, MongoSpanDoc{
			TraceID:      sc.TraceID().String(),
			SpanID:       sc.SpanID().String(),
			ParentSpanID: parentID,
			Name:         s.Name(),
			Service:      serviceName,
			Kind:         s.SpanKind().String(),
			StartTime:    s.StartTime(),
			EndTime:      s.EndTime(),
			DurationMs:   duration.Milliseconds(),
			StatusCode:   s.Status().Code.String(),
			StatusDesc:   s.Status().Description,
			Attributes:   attrs,
		})
	}

	// Use an unordered bulk insert so a single failing doc (rare —
	// shouldn't happen with our schema) doesn't block the rest of the
	// batch. InsertMany is faster than per-doc Inserts at batch sizes
	// the SDK gives us (typically 100–500 spans).
	insertOpts := options.InsertMany().SetOrdered(false)
	if _, err := e.coll.InsertMany(ctx, docs, insertOpts); err != nil {
		log.Printf("⚠️ [TRACING-MONGO] bulk insert failed for %d span(s): %v", len(docs), err)
		// Swallow — don't fail OTel pipeline.
	}
	return nil
}

// Shutdown drains pending work. Our writes are synchronous within
// ExportSpans, so there's nothing to drain — just mark closed.
func (e *MongoSpanExporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	return nil
}

// asSpanContext silences "imported and not used" if `trace` package is
// only referenced indirectly. Kept for clarity if future code needs the
// explicit interface — costs nothing at runtime.
var _ = trace.SpanContext{}
