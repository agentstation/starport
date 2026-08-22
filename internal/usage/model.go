// Package usage owns the canonical per-request usage record: what one
// inference request consumed, where it ran, and what it cost. Records are
// written best-effort after request completion and feed the activity API,
// the console usage page, and budget enforcement.
package usage

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Statuses classify how a request finished.
const (
	// StatusOK reports a completed request.
	StatusOK = "ok"
	// StatusError reports a failed request.
	StatusError = "error"
	// StatusCancelled reports a client-cancelled request.
	StatusCancelled = "cancelled"
)

// Operations name the inference surface a record measures.
const (
	// OperationChat reports a chat completion request.
	OperationChat = "chat"
	// OperationEmbeddings reports an embeddings request.
	OperationEmbeddings = "embeddings"
)

// Cost unavailability reasons. A record without a cost carries one so the
// gap is loud, never a silent zero.
const (
	// CostReasonNoPricing means the catalog offering had no pricing data.
	CostReasonNoPricing = "no_pricing"
	// CostReasonNoRoute means the request failed before a route was chosen.
	CostReasonNoRoute = "no_route"
	// CostReasonNoUsage means the provider returned no token counts.
	CostReasonNoUsage = "no_usage"
)

// ErrInvalidRecord reports a record that cannot be persisted.
var ErrInvalidRecord = errors.New("invalid usage record")

// Tokens holds provider-reported token counts for one request.
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Total      int64 `json:"total"`
	Reasoning  int64 `json:"reasoning,omitempty"`
	CacheRead  int64 `json:"cache_read,omitempty"`
	CacheWrite int64 `json:"cache_write,omitempty"`
}

// Cost is the Starmap-derived cost of one request in integer nano-USD.
type Cost struct {
	NanoUSD  int64  `json:"nano_usd"`
	Currency string `json:"currency"`
}

// Record is one completed inference request.
type Record struct {
	RequestID      string    `json:"request_id"`
	KeyID          string    `json:"key_id"`
	Timestamp      time.Time `json:"timestamp"`
	Protocol       string    `json:"protocol,omitempty"`
	Operation      string    `json:"operation"`
	ModelRequested string    `json:"model_requested,omitempty"`
	ModelUsed      string    `json:"model_used,omitempty"`
	Provider       string    `json:"provider,omitempty"`
	Streaming      bool      `json:"streaming,omitempty"`
	Status         string    `json:"status"`
	StatusCode     int       `json:"status_code,omitempty"`
	ErrorClass     string    `json:"error_class,omitempty"`
	Tokens         Tokens    `json:"tokens"`
	// TokensEstimated marks counts the gateway synthesized with a
	// tokenizer because the provider reported none.
	TokensEstimated bool `json:"tokens_estimated,omitempty"`
	LatencyMS      int64     `json:"latency_ms"`
	RoutingMS      int64     `json:"routing_ms,omitempty"`
	// OverheadMS is the gateway-added latency: total handling time
	// minus upstream provider waits.
	OverheadMS     int64     `json:"overhead_ms,omitempty"`
	Attempts       int       `json:"attempts,omitempty"`
	CacheStatus    string    `json:"cache_status,omitempty"`

	// Cost is nil when no cost could be computed; CostUnavailableReason
	// then names why.
	Cost                  *Cost  `json:"cost,omitempty"`
	CostUnavailableReason string `json:"cost_unavailable_reason,omitempty"`
}

// Validate reports whether the record can be persisted.
func (r Record) Validate() error {
	if strings.TrimSpace(r.RequestID) == "" {
		return fmt.Errorf("%w: request id is required", ErrInvalidRecord)
	}
	if strings.TrimSpace(r.KeyID) == "" {
		return fmt.Errorf("%w: key id is required", ErrInvalidRecord)
	}
	if r.Timestamp.IsZero() {
		return fmt.Errorf("%w: timestamp is required", ErrInvalidRecord)
	}
	if strings.TrimSpace(r.Operation) == "" {
		return fmt.Errorf("%w: operation is required", ErrInvalidRecord)
	}
	switch r.Status {
	case StatusOK, StatusError, StatusCancelled:
	default:
		return fmt.Errorf("%w: unknown status %q", ErrInvalidRecord, r.Status)
	}
	if r.Cost == nil && r.CostUnavailableReason == "" {
		return fmt.Errorf("%w: a record needs a cost or a cost unavailable reason", ErrInvalidRecord)
	}
	if r.Cost != nil && r.Cost.Currency == "" {
		return fmt.Errorf("%w: cost currency is required", ErrInvalidRecord)
	}
	return nil
}
