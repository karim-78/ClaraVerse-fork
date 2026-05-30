package handlers

// ============================================================================
// Admin trace viewer endpoints.
//
//   GET  /api/admin/traces?limit=50&since=15m&service=...&status=error
//        Returns the most-recent root spans (one per execution).
//
//   GET  /api/admin/traces/:trace_id
//        Returns ALL spans for the trace, ordered by start_time. The
//        frontend uses this to build the waterfall.
//
// Auth: routes are mounted inside the admin group, which requires
// SUPERADMIN_USER_IDS membership.

import (
	"claraverse/internal/database"
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type TracesAdminHandler struct {
	coll *mongo.Collection
}

func NewTracesAdminHandler(db *database.MongoDB) *TracesAdminHandler {
	return &TracesAdminHandler{coll: db.Collection("workflow_traces")}
}

// traceListItem is the shape sent to the UI for the list view. Slim — we
// don't ship attributes here (the detail call does).
type traceListItem struct {
	TraceID    string    `json:"trace_id"`
	Name       string    `json:"name"`
	Service    string    `json:"service"`
	StartTime  time.Time `json:"start_time"`
	DurationMs int64     `json:"duration_ms"`
	StatusCode string    `json:"status_code"`
	StatusDesc string    `json:"status_desc,omitempty"`
	// Convenience fields lifted from attributes so the table can render
	// without each row decoding the full attribute map.
	ExecutionID string `json:"execution_id,omitempty"`
	WorkflowID  string `json:"workflow_id,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	BlockCount  int    `json:"block_count,omitempty"`
}

// List returns the most-recent root spans (parent_span_id="") matching the
// filters. Use this to power the admin "Traces" tab.
func (h *TracesAdminHandler) List(c *fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	// since: e.g. "15m", "1h", "24h". Defaults to last 24h to keep the
	// table fast — the TTL is 14d so the data is there if needed.
	sinceStr := c.Query("since", "24h")
	since, err := time.ParseDuration(sinceStr)
	if err != nil || since <= 0 {
		since = 24 * time.Hour
	}
	cutoff := time.Now().Add(-since)

	filter := bson.M{
		"parent_span_id": "",
		"start_time":     bson.M{"$gte": cutoff},
	}
	if svc := strings.TrimSpace(c.Query("service")); svc != "" {
		filter["service"] = svc
	}
	if st := strings.TrimSpace(c.Query("status")); st != "" {
		// Map "error" to OTel's "Error" status code string.
		switch strings.ToLower(st) {
		case "error", "errors":
			filter["status_code"] = "Error"
		case "ok":
			filter["status_code"] = "Ok"
		case "unset":
			filter["status_code"] = "Unset"
		}
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "start_time", Value: -1}}).
		SetLimit(int64(limit))

	cursor, err := h.coll.Find(c.Context(), filter, opts)
	if err != nil {
		log.Printf("❌ [TRACES-ADMIN] list query failed: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	defer cursor.Close(c.Context())

	items := make([]traceListItem, 0, limit)
	for cursor.Next(c.Context()) {
		var raw struct {
			TraceID    string                 `bson:"trace_id"`
			Name       string                 `bson:"name"`
			Service    string                 `bson:"service"`
			StartTime  time.Time              `bson:"start_time"`
			DurationMs int64                  `bson:"duration_ms"`
			StatusCode string                 `bson:"status_code"`
			StatusDesc string                 `bson:"status_desc"`
			Attributes map[string]interface{} `bson:"attributes"`
		}
		if err := cursor.Decode(&raw); err != nil {
			continue
		}
		item := traceListItem{
			TraceID:    raw.TraceID,
			Name:       raw.Name,
			Service:    raw.Service,
			StartTime:  raw.StartTime,
			DurationMs: raw.DurationMs,
			StatusCode: raw.StatusCode,
			StatusDesc: raw.StatusDesc,
		}
		if v, ok := raw.Attributes["execution.id"].(string); ok {
			item.ExecutionID = v
		}
		if v, ok := raw.Attributes["workflow.id"].(string); ok {
			item.WorkflowID = v
		}
		if v, ok := raw.Attributes["user.id"].(string); ok {
			item.UserID = v
		}
		// block_count comes in as int64 from Mongo
		switch v := raw.Attributes["workflow.block_count"].(type) {
		case int32:
			item.BlockCount = int(v)
		case int64:
			item.BlockCount = int(v)
		case float64:
			item.BlockCount = int(v)
		}
		items = append(items, item)
	}

	return c.JSON(fiber.Map{
		"traces":     items,
		"window_sec": int(since.Seconds()),
		"count":      len(items),
	})
}

// Get returns every span for a single trace. The frontend uses this to
// render the waterfall — orders by start_time so the array is already
// timeline-ordered.
func (h *TracesAdminHandler) Get(c *fiber.Ctx) error {
	traceID := c.Params("trace_id")
	if traceID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "trace_id required"})
	}

	opts := options.Find().SetSort(bson.D{{Key: "start_time", Value: 1}})
	cursor, err := h.coll.Find(c.Context(), bson.M{"trace_id": traceID}, opts)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	defer cursor.Close(c.Context())

	type spanOut struct {
		TraceID      string                 `json:"trace_id"`
		SpanID       string                 `json:"span_id"`
		ParentSpanID string                 `json:"parent_span_id"`
		Name         string                 `json:"name"`
		Service      string                 `json:"service"`
		Kind         string                 `json:"kind"`
		StartTime    time.Time              `json:"start_time"`
		EndTime      time.Time              `json:"end_time"`
		DurationMs   int64                  `json:"duration_ms"`
		StatusCode   string                 `json:"status_code"`
		StatusDesc   string                 `json:"status_desc,omitempty"`
		Attributes   map[string]interface{} `json:"attributes,omitempty"`
	}
	spans := []spanOut{}
	if err := cursor.All(c.Context(), &spans); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	if len(spans) == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "trace not found"})
	}

	// Compute the trace's overall window so the UI can scale the waterfall.
	// Also roll up per-block cost (block.cost_usd attribute) + token totals
	// so the trace detail header can show $cost / total tokens without the
	// UI having to walk every span.
	var traceStart, traceEnd time.Time
	totalCostUSD := 0.0
	totalInputTokens := 0
	totalOutputTokens := 0
	pricedBlocks := 0
	llmBlocks := 0
	for i, s := range spans {
		if i == 0 || s.StartTime.Before(traceStart) {
			traceStart = s.StartTime
		}
		if i == 0 || s.EndTime.After(traceEnd) {
			traceEnd = s.EndTime
		}
		if s.Attributes == nil {
			continue
		}
		if v, ok := numericAttr(s.Attributes, "block.cost_usd"); ok {
			totalCostUSD += v
			pricedBlocks++
		}
		if v, ok := numericAttr(s.Attributes, "llm.input_tokens"); ok {
			totalInputTokens += int(v)
			llmBlocks++
		}
		if v, ok := numericAttr(s.Attributes, "llm.output_tokens"); ok {
			totalOutputTokens += int(v)
		}
	}

	return c.JSON(fiber.Map{
		"trace_id":          traceID,
		"span_count":        len(spans),
		"trace_start":       traceStart,
		"trace_end":         traceEnd,
		"trace_duration_ms": traceEnd.Sub(traceStart).Milliseconds(),
		"spans":             spans,
		"cost_usd":          totalCostUSD,
		"cost_partial":      llmBlocks > pricedBlocks, // some LLM blocks couldn't be priced
		"input_tokens":      totalInputTokens,
		"output_tokens":     totalOutputTokens,
		"llm_blocks":        llmBlocks,
	})
}

// numericAttr defensively converts Mongo's various numeric encodings
// (int32, int64, float64) into a float64. Returns ok=false for missing
// or non-numeric attributes.
func numericAttr(attrs map[string]interface{}, key string) (float64, bool) {
	v, ok := attrs[key]
	if !ok || v == nil {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// Healthcheck-friendly stats endpoint: how many traces in the last hour,
// p50/p95 duration, error rate. Used by the admin tab's summary header.
func (h *TracesAdminHandler) Stats(c *fiber.Ctx) error {
	cutoff := time.Now().Add(-1 * time.Hour)
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()

	pipeline := bson.A{
		bson.M{"$match": bson.M{
			"parent_span_id": "",
			"start_time":     bson.M{"$gte": cutoff},
		}},
		bson.M{"$group": bson.M{
			"_id":    nil,
			"total":  bson.M{"$sum": 1},
			"errors": bson.M{"$sum": bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$status_code", "Error"}}, 1, 0}}},
			"p50":    bson.M{"$avg": "$duration_ms"}, // not a true p50 but cheap; swap for $percentile when we move to Mongo 7+
			"max":    bson.M{"$max": "$duration_ms"},
		}},
	}
	cursor, err := h.coll.Aggregate(ctx, pipeline)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	defer cursor.Close(ctx)
	var rows []struct {
		Total  int64   `bson:"total"`
		Errors int64   `bson:"errors"`
		P50    float64 `bson:"p50"`
		Max    int64   `bson:"max"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if len(rows) == 0 {
		return c.JSON(fiber.Map{
			"window_minutes": 60,
			"total":          0,
			"errors":         0,
			"error_rate":     0.0,
			"avg_ms":         0,
			"max_ms":         0,
		})
	}
	r := rows[0]
	errorRate := 0.0
	if r.Total > 0 {
		errorRate = float64(r.Errors) / float64(r.Total)
	}
	return c.JSON(fiber.Map{
		"window_minutes": 60,
		"total":          r.Total,
		"errors":         r.Errors,
		"error_rate":     errorRate,
		"avg_ms":         int(r.P50),
		"max_ms":         r.Max,
	})
}

// ensureMongo nil-guards admin endpoints when Mongo isn't wired. Kept as
// a helper so the routes file stays tiny.
func (h *TracesAdminHandler) ensureMongo(c *fiber.Ctx) error {
	if h == nil || h.coll == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "trace viewer requires MongoDB"})
	}
	return nil
}

// Hint for tools/lint: silence the unused-import warning on strconv when
// a future refactor removes the only caller. Safe no-op.
var _ = fmt.Sprintf
