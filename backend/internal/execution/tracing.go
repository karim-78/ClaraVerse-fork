package execution

// ============================================================================
// Workflow tracing — OpenTelemetry
//
// One trace per execution. Each block is a child span. Attributes carry
// execution_id, workflow_id, user_id, block_id, block_type, block_name,
// whether the work was actually performed or served from cache, and error
// detail when applicable.
//
// Bootstrap (in main.go on startup):
//
//   execution.InitTracing(execution.TracingConfig{
//       ServiceName: "dobbyai-backend",
//       Endpoint:    os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
//       Headers:     execution.ParseOTLPHeaders(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")),
//   })
//
// When OTEL_EXPORTER_OTLP_ENDPOINT is unset, traces fall back to a stdout
// exporter so engineers can still see spans during local dev. When the env
// is set (e.g. http://otel-collector:4318), traces ship to the collector
// via OTLP-HTTP and flow into Tempo/Honeycomb/Datadog/etc.
//
// The exporter is global. Calling InitTracing more than once is safe but
// only the first call takes effect — repeated calls just log a warning.

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// TracingConfig is the operator-facing config knob.
type TracingConfig struct {
	ServiceName    string            // e.g. "dobbyai-backend"
	ServiceVersion string            // e.g. "v1.2.3" (defaults to "dev")
	Endpoint       string            // OTLP HTTP endpoint, e.g. "otel-collector:4318". Empty = stdout exporter.
	Insecure       bool              // true for HTTP (collector inside docker net); false for HTTPS
	Headers        map[string]string // e.g. {"x-honeycomb-team": "abc"}
	// ExtraExporters lets the caller register additional span exporters
	// alongside the default OTLP/stdout one. We use this to add the
	// MongoSpanExporter so the built-in admin trace viewer always has
	// data, even when an external collector is also configured.
	ExtraExporters []sdktrace.SpanExporter
}

var (
	tracingMu      sync.Mutex
	tracingStarted bool
	tracerProvider *sdktrace.TracerProvider
	tracer         trace.Tracer
)

// InitTracing wires the global tracer. Safe to call before the rest of the
// app is up — span creation is a no-op until this returns. Idempotent.
func InitTracing(cfg TracingConfig) error {
	tracingMu.Lock()
	defer tracingMu.Unlock()
	if tracingStarted {
		log.Println("ℹ️ [TRACING] already initialised — skipping")
		return nil
	}

	if cfg.ServiceName == "" {
		cfg.ServiceName = "dobbyai-backend"
	}
	if cfg.ServiceVersion == "" {
		cfg.ServiceVersion = "dev"
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return err
	}

	var exporter sdktrace.SpanExporter
	if cfg.Endpoint != "" {
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(stripScheme(cfg.Endpoint)),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
		}
		exporter, err = otlptrace.New(context.Background(), otlptracehttp.NewClient(opts...))
		if err != nil {
			return err
		}
		log.Printf("📡 [TRACING] OTLP HTTP exporter → %s", cfg.Endpoint)
	} else {
		// Local dev fallback. PrettyPrint(false) keeps line noise down.
		exporter, err = stdouttrace.New(stdouttrace.WithoutTimestamps())
		if err != nil {
			return err
		}
		log.Println("📡 [TRACING] stdout exporter (set OTEL_EXPORTER_OTLP_ENDPOINT to ship to a collector)")
	}

	bsp := sdktrace.NewBatchSpanProcessor(exporter,
		sdktrace.WithMaxQueueSize(2048),
		sdktrace.WithMaxExportBatchSize(512),
		sdktrace.WithBatchTimeout(5*time.Second),
	)

	// Build the provider with the primary processor + any extras (e.g.
	// the MongoSpanExporter that powers the built-in admin trace viewer).
	providerOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	}
	for _, extra := range cfg.ExtraExporters {
		extraBSP := sdktrace.NewBatchSpanProcessor(extra,
			sdktrace.WithMaxQueueSize(2048),
			sdktrace.WithMaxExportBatchSize(256),
			sdktrace.WithBatchTimeout(5*time.Second),
		)
		providerOpts = append(providerOpts, sdktrace.WithSpanProcessor(extraBSP))
	}

	// Always-on sampling for workflow traces. Production deployments with
	// high volume should swap this for ParentBased(TraceIDRatioBased(0.1))
	// once the operator has a baseline of how many traces they generate.
	providerOpts = append(providerOpts, sdktrace.WithSampler(sdktrace.AlwaysSample()))
	tracerProvider = sdktrace.NewTracerProvider(providerOpts...)
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	tracer = tracerProvider.Tracer("claraverse/execution")
	tracingStarted = true
	return nil
}

// ShutdownTracing flushes pending spans + releases the exporter. Call on
// graceful shutdown (defer in main.go).
func ShutdownTracing(ctx context.Context) {
	tracingMu.Lock()
	defer tracingMu.Unlock()
	if tracerProvider == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := tracerProvider.Shutdown(shutdownCtx); err != nil {
		log.Printf("⚠️ [TRACING] shutdown error: %v", err)
	}
}

// ParseOTLPHeaders accepts the standard OTEL header syntax
// ("key1=value1,key2=value2") and returns a map. Empty input → nil map.
func ParseOTLPHeaders(s string) map[string]string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := make(map[string]string)
	for _, kv := range strings.Split(s, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		eq := strings.Index(kv, "=")
		if eq <= 0 {
			continue
		}
		out[strings.TrimSpace(kv[:eq])] = strings.TrimSpace(kv[eq+1:])
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stripScheme normalises endpoints by removing http:// or https:// — the
// OTLP-HTTP client wants host[:port] only and uses Insecure to choose
// between http/https.
func stripScheme(endpoint string) string {
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimSuffix(endpoint, "/")
	return endpoint
}

// ─── span helpers ──────────────────────────────────────────────────────

// StartExecutionSpan opens the root span for a workflow execution. Returns
// a no-op span when tracing isn't initialised so callers can use
// `defer span.End()` unconditionally.
func StartExecutionSpan(ctx context.Context, executionID, workflowID, userID string, blockCount int) (context.Context, trace.Span) {
	if tracer == nil {
		return ctx, noopSpan{}
	}
	return tracer.Start(ctx, "workflow.execute",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("execution.id", executionID),
			attribute.String("workflow.id", workflowID),
			attribute.String("user.id", userID),
			attribute.Int("workflow.block_count", blockCount),
		),
	)
}

// StartBlockSpan opens a child span for a single block. CacheServed=true
// reflects that the work was a checkpoint replay (no real work happened).
func StartBlockSpan(ctx context.Context, blockID, blockType, blockName string, cacheServed bool) (context.Context, trace.Span) {
	if tracer == nil {
		return ctx, noopSpan{}
	}
	return tracer.Start(ctx, "block."+blockType,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("block.id", blockID),
			attribute.String("block.type", blockType),
			attribute.String("block.name", blockName),
			attribute.Bool("block.cache_served", cacheServed),
		),
	)
}

// currentSpan returns the span attached to ctx, or nil if tracing isn't
// initialised / no span is in ctx. Callers use this to attach attributes
// from inside a block executor without needing to thread the span explicitly.
func currentSpan(ctx context.Context) trace.Span {
	sp := trace.SpanFromContext(ctx)
	if sp == nil || !sp.IsRecording() {
		return nil
	}
	return sp
}

// annotateBlockSpan attaches LLM accounting attributes (model, tokens,
// $ cost) to a block span. The trace viewer's per-execution cost rollup
// sums block.cost_usd across all spans of a trace.
func annotateBlockSpan(span trace.Span, model string, inputTokens, outputTokens int, costUSD float64) {
	if span == nil {
		return
	}
	span.SetAttributes(
		attribute.String("llm.model", model),
		attribute.Int("llm.input_tokens", inputTokens),
		attribute.Int("llm.output_tokens", outputTokens),
		attribute.Float64("block.cost_usd", costUSD),
	)
}

// AnnotateSpanError marks a span as errored with a message. Used both by
// the engine wrap-up and by tools that detect downstream failure inside an
// otherwise-OK block.
func AnnotateSpanError(span trace.Span, message string) {
	if span == nil {
		return
	}
	span.SetStatus(codes.Error, message)
	span.SetAttributes(attribute.String("error.message", message))
}

// noopSpan is the stub returned when tracing isn't initialised — keeps
// callers from needing `if span != nil` guards everywhere.
type noopSpan struct {
	trace.Span
}

func (noopSpan) End(...trace.SpanEndOption)                  {}
func (noopSpan) AddEvent(string, ...trace.EventOption)       {}
func (noopSpan) IsRecording() bool                           { return false }
func (noopSpan) RecordError(error, ...trace.EventOption)     {}
func (noopSpan) SetStatus(codes.Code, string)                {}
func (noopSpan) SetName(string)                              {}
func (noopSpan) SetAttributes(...attribute.KeyValue)         {}
func (noopSpan) TracerProvider() trace.TracerProvider        { return otel.GetTracerProvider() }
func (noopSpan) SpanContext() trace.SpanContext              { return trace.SpanContext{} }
func (noopSpan) AddLink(trace.Link)                          {}

// ApplyEnvDefaults pulls tracing config from standard OTEL env vars so
// operators get the conventional behavior with no code changes. Called by
// the bootstrap path in main.go.
func ApplyEnvDefaults() TracingConfig {
	return TracingConfig{
		ServiceName:    envOr("OTEL_SERVICE_NAME", "dobbyai-backend"),
		ServiceVersion: envOr("DOBBYAI_VERSION", "dev"),
		Endpoint:       os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		Insecure:       strings.ToLower(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE")) == "true",
		Headers:        ParseOTLPHeaders(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS")),
	}
}

func envOr(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}
