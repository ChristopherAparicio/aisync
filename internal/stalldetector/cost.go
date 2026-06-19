package stalldetector

import "github.com/ChristopherAparicio/aisync/internal/pricing"

// estimateCost computes the dollar cost lost on a stalled/errored
// message when the provider's own `cost` field is unavailable
// (subscription plans, partial captures). Falls back to 0 when the
// catalog has no entry for the model.
//
// Formula:
//
//	cost = (input_billed × input_rate + output × output_rate) / 1_000_000
//
// where input_billed = raw_input + cache_read + cache_write, matching
// the convention used by internal/provider/opencode/opencode.go
// (sumTokens) and the rest of aisync's cost pipeline.
//
// Tiered pricing (PricingTier) is intentionally NOT applied here:
// stalls capture single messages, and the tier discount only matters
// for cumulative session totals which the aggregator computes
// separately. Treating each errored message at base rate slightly
// over-estimates large-context aborts — acceptable for a lost-cost
// upper bound.
func estimateCost(cat pricing.Catalog, model string, rawInput, output, cacheRead, cacheWrite int64) float64 {
	if cat == nil || model == "" {
		return 0
	}
	price, ok := cat.Lookup(model)
	if !ok {
		return 0
	}
	inputBilled := rawInput + cacheRead + cacheWrite
	const perMillion = 1_000_000.0
	return (float64(inputBilled)*price.InputPerMToken + float64(output)*price.OutputPerMToken) / perMillion
}
