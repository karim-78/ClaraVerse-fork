package execution

// ============================================================================
// Per-block cost estimation.
//
// Mirrors services.estimateCostUSD so workflow LLM blocks can attach a $
// figure to their span attribute (block.cost_usd) without having to import
// chat_service. The trace viewer sums these to show a per-execution rollup.
//
// Keep this list in sync with services/chat_service.go modelPricing. When
// the catalogue gets larger or moves to a DB-backed model registry we'll
// promote it to a shared package; for now duplication is intentional and
// trivially auditable (six entries on each side).
//
// Prices are USD per 1M tokens, accurate as of May 2026.

var blockPricing = map[string]struct{ inUSD, cachedUSD, outUSD float64 }{
	"qwen.qwen3-32b-v1:0":             {0.15, 0.015, 0.60},
	"qwen.qwen3-coder-480b-a35b-v1:0": {2.00, 0.20, 6.00},
	"moonshotai.kimi-k2.5":            {0.60, 0.06, 2.50},
	"zai.glm-5":                       {0.60, 0.06, 2.20},
	"openai.gpt-oss-20b-1:0":          {0.10, 0.01, 0.40},
	"openai.gpt-oss-120b-1:0":         {0.60, 0.06, 2.40},
}

// EstimateBlockCostUSD totals the cost of one block invocation. Returns 0
// when we don't have a price entry for the model — the trace viewer
// renders the per-execution total as `$0.00 (partial)` in that case so
// users know we couldn't price everything.
func EstimateBlockCostUSD(model string, freshInput, cached, output int) float64 {
	p, ok := blockPricing[model]
	if !ok {
		return 0
	}
	const oneMillion = 1_000_000.0
	c := float64(freshInput)*p.inUSD/oneMillion +
		float64(cached)*p.cachedUSD/oneMillion +
		float64(output)*p.outUSD/oneMillion
	if c < 0 {
		return 0
	}
	return c
}
