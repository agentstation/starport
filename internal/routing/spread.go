package routing

import "math/rand/v2"

// DefaultSpreadRatio bounds the spread band when the request does not name a
// ratio: a candidate joins the band while its ranking metric stays within
// 25% of the best candidate's metric.
const DefaultSpreadRatio = 1.25

// spreadRankedCandidates reorders the leading sorted candidates when the
// request asks for spread. The band is the unbroken run of candidates that
// share the best candidate's rank tier and whose ranking metric sits within
// the configured ratio of the best. Inside the band the order is a weighted
// draw without replacement, seeded by the request, with weight inverse to the
// metric so a cheaper or faster route still carries more traffic. Candidates
// outside the band keep the deterministic order behind the band.
func spreadRankedCandidates(items []rankedCandidate, optimization OptimizationPolicy, hasProviderOrder bool) {
	if !optimization.Spread || len(items) < 2 {
		return
	}
	band := spreadBandLength(items, optimization, hasProviderOrder)
	if band < 2 {
		return
	}
	orderBandWeighted(items[:band], optimization)
}

// spreadBandLength measures the band from the head of the sorted candidates.
// The band never crosses a rank tier: a lower-ranked model, an out-of-order
// provider, or a lost affinity match ends it, because the deterministic sort
// placed those tiers behind the best on purpose. A candidate without the
// active metric also ends the band, because the ratio promise cannot be
// checked against an unknown value.
func spreadBandLength(items []rankedCandidate, optimization OptimizationPolicy, hasProviderOrder bool) int {
	best := items[0].evidence
	bestMetric, known := spreadMetric(best, optimization)
	if !known {
		return 1
	}
	ratio := optimization.SpreadRatio
	if ratio < 1 {
		ratio = DefaultSpreadRatio
	}
	bound := bestMetric * ratio
	length := 1
	for _, item := range items[1:] {
		evidence := item.evidence
		if evidence.ModelRank != best.ModelRank {
			break
		}
		if hasProviderOrder && evidence.ProviderRank != best.ProviderRank {
			break
		}
		if evidence.AffinityMatched != best.AffinityMatched {
			break
		}
		metric, known := spreadMetric(evidence, optimization)
		if !known || metric > bound {
			break
		}
		length++
	}
	return length
}

// spreadMetric names the value the band is measured in. It mirrors the
// deterministic sort's precedence: cost when the request prefers cost and the
// candidate has one, then latency.
func spreadMetric(evidence SelectionEvidence, optimization OptimizationPolicy) (float64, bool) {
	if optimization.PreferLowestCost && evidence.HasCost {
		return evidence.EstimatedCost, true
	}
	if optimization.PreferLowestLatency && evidence.HasLatency {
		return float64(evidence.EstimatedLatency), true
	}
	return 0, false
}

// orderBandWeighted draws the band order without replacement. The band head
// holds the minimum metric, so every weight is positive: a zero best metric
// bounds the band to zero-metric candidates, which draw with equal weight.
func orderBandWeighted(band []rankedCandidate, optimization OptimizationPolicy) {
	// Route spread balances load; it is not a security boundary, so a seeded
	// deterministic generator is the point rather than a weakness.
	source := rand.New(rand.NewPCG(optimization.SpreadSeed, uint64(len(band)))) // #nosec G404 -- seeded spread is deliberate.
	bestMetric, _ := spreadMetric(band[0].evidence, optimization)
	weights := make([]float64, len(band))
	for index, item := range band {
		metric, _ := spreadMetric(item.evidence, optimization)
		if bestMetric > 0 {
			weights[index] = bestMetric / metric
		} else {
			weights[index] = 1
		}
	}
	for position := range band[:len(band)-1] {
		total := 0.0
		for _, weight := range weights[position:] {
			total += weight
		}
		pick := source.Float64() * total
		chosen := position
		for index := position; index < len(band); index++ {
			pick -= weights[index]
			if pick < 0 {
				chosen = index
				break
			}
		}
		band[position], band[chosen] = band[chosen], band[position]
		weights[position], weights[chosen] = weights[chosen], weights[position]
	}
}
