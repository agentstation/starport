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
	// OperationImages reports an image generation or image edit request.
	// One name covers both, because the two are metered the same way and a
	// spend report reads the meter rather than the path.
	OperationImages = "images"
	// OperationSpeech reports a text-to-speech request.
	OperationSpeech = "speech"
	// OperationTranscription reports a speech-to-text request, in the spoken
	// language or translated.
	OperationTranscription = "transcription"
	// OperationVideos reports a video generation. It is the one operation whose
	// record is written after the request that started it returned, because the
	// work outlives that request and only its end states what it cost.
	OperationVideos = "videos"
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
	// CostReasonMediaUnpriced means the turn carried a media unit the
	// offering does not price. The token half of such a turn does have a
	// price, so a cost is computable; it would omit the media half, which is
	// the expensive one. A silent understatement is worse than a named gap,
	// so the whole cost drops and this reason says why.
	CostReasonMediaUnpriced = "media_unpriced"
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
	// AudioInput and AudioOutput are the audio shares of Input and Output,
	// not additions to them. A provider meters audio at its own rate, so a
	// cost reclassifies these out of the plain rates rather than adding them.
	AudioInput  int64 `json:"audio_input,omitempty"`
	AudioOutput int64 `json:"audio_output,omitempty"`
}

// Media counts the non-token units one request produced. A generated image
// carries no token count at all, so a token total cannot describe it, and a
// spend budget that reads tokens alone would meter such a turn as free.
type Media struct {
	GeneratedImages int64 `json:"generated_images,omitempty"`
	// GeneratedVideos counts finished videos. A provider prices a video per
	// video, not per second and not per token, so this is the whole meter for
	// the operation rather than a share of another one.
	GeneratedVideos int64 `json:"generated_videos,omitempty"`
}

// Cost is the Starmap-derived cost of one request in integer nano-USD.
type Cost struct {
	NanoUSD  int64  `json:"nano_usd"`
	Currency string `json:"currency"`
}

// Record is one completed inference request.
type Record struct {
	RequestID string `json:"request_id"`
	KeyID     string `json:"key_id"`
	// TenantID is the account the key belongs to. It is what an account-wide
	// spend cap counts, and a key ID cannot stand in for it because an account
	// holds many keys. It is optional so that a record written before account
	// attribution stays readable; such a record counts toward no account.
	TenantID       string    `json:"tenant_id,omitempty"`
	Timestamp      time.Time `json:"timestamp"`
	Protocol       string    `json:"protocol,omitempty"`
	Operation      string    `json:"operation"`
	ModelRequested string    `json:"model_requested,omitempty"`
	ModelUsed      string    `json:"model_used,omitempty"`
	Provider       string    `json:"provider,omitempty"`
	// CredentialSource names which credential plane paid for the request:
	// `environment`, `gateway`, `byok`, or `anonymous` for a provider that
	// accepted the call without one. It is what lets an operator see an
	// account drawing on the deployment's credential rather than its own. It
	// is empty on a record written before a route was chosen, and on one
	// written before the gateway recorded the plane.
	CredentialSource string `json:"credential_source,omitempty"`
	Streaming        bool   `json:"streaming,omitempty"`
	Status           string `json:"status"`
	StatusCode       int    `json:"status_code,omitempty"`
	ErrorClass       string `json:"error_class,omitempty"`
	Tokens           Tokens `json:"tokens"`
	// Media is nil on a text turn, which is what every record written before
	// media accounting existed reads back as.
	Media *Media `json:"media,omitempty"`
	// TokensEstimated marks counts the gateway synthesized with a
	// tokenizer because the provider reported none.
	TokensEstimated bool  `json:"tokens_estimated,omitempty"`
	LatencyMS       int64 `json:"latency_ms"`
	RoutingMS       int64 `json:"routing_ms,omitempty"`
	// OverheadMS is the gateway-added latency: total handling time
	// minus upstream provider waits.
	OverheadMS int64 `json:"overhead_ms,omitempty"`
	// TTFTMS is the time from request start to the first stream event.
	// Only streamed requests carry it.
	TTFTMS      int64  `json:"ttft_ms,omitempty"`
	Attempts    int    `json:"attempts,omitempty"`
	CacheStatus string `json:"cache_status,omitempty"`

	// ParserEngine names which engine read the documents this turn attached:
	// `native` for the in-process reader, `recognition` for a catalogued model.
	// It is empty on a turn that attached none.
	ParserEngine string `json:"parser_engine,omitempty"`
	// DocumentPages is how many pages those attachments held, whether this turn
	// read them or the cache answered for them.
	DocumentPages int64 `json:"document_pages,omitempty"`
	// RecognizedPages is how many of those pages this turn sent to a
	// recognition model. It is what ExtractionCost is charged for, and it is
	// zero on a cached read: the pages were recognized once, on an earlier turn
	// that paid for them.
	RecognizedPages int64 `json:"recognized_pages,omitempty"`
	// NativePages is how many pages this turn read in process. They cost
	// nothing: no provider saw them.
	NativePages int64 `json:"native_pages,omitempty"`
	// ExtractionCached reports that every attachment came back from the
	// extraction cache. A cached read and a native read both record no cost,
	// and only this field separates a page an earlier turn already paid for
	// from a page no provider ever charged for.
	ExtractionCached bool `json:"extraction_cached,omitempty"`
	// ExtractionMillis is how long the document reads took. A recognition read
	// is a provider call inside a provider call, so it is latency an operator
	// cannot find anywhere else in this record.
	ExtractionMillis int64 `json:"extraction_millis,omitempty"`
	// ExtractionCost is the recognized share of Cost below, reported on its own
	// so an operator can see what reading a document cost apart from what
	// answering about it cost. It is nil when the turn recognized nothing.
	ExtractionCost *Cost `json:"extraction_cost,omitempty"`

	// Cost is nil when no cost could be computed; CostUnavailableReason
	// then names why. It covers the whole request, the document reads
	// included.
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
