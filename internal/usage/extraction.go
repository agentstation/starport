package usage

import (
	"math"
	"time"
)

// Extraction records one fresh provider recognition call. Cache hits add no call.
type Extraction struct {
	StartedAt             time.Time `json:"started_at,omitzero"`
	Offering              string    `json:"offering"`
	GenerationID          string    `json:"generation_id,omitempty"`
	Pages                 int64     `json:"pages"`
	BillingBasis          string    `json:"billing_basis,omitempty"`
	Tokens                *Tokens   `json:"tokens,omitempty"`
	Cost                  *Cost     `json:"cost,omitempty"`
	CostUnavailableReason string    `json:"cost_unavailable_reason,omitempty"`
}

// Clone copies measurements and cost so asynchronous persistence owns its data.
func (e Extraction) Clone() Extraction {
	cloned := e
	if e.Tokens != nil {
		tokens := *e.Tokens
		cloned.Tokens = &tokens
	}
	if e.Cost != nil {
		cost := *e.Cost
		cloned.Cost = &cost
	}
	return cloned
}

func (r Record) knownSpendNanoUSD() int64 {
	if r.Cost != nil {
		return r.Cost.NanoUSD
	}
	if len(r.Extractions) == 0 {
		if r.ExtractionCost != nil {
			return r.ExtractionCost.NanoUSD
		}
		return 0
	}
	var subtotal int64
	for _, entry := range r.Extractions {
		if entry.Cost == nil || entry.Cost.NanoUSD <= 0 {
			continue
		}
		if entry.Cost.NanoUSD > math.MaxInt64-subtotal {
			return math.MaxInt64
		}
		subtotal += entry.Cost.NanoUSD
	}
	return subtotal
}
