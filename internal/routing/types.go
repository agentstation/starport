// Package routing plans deterministic provider attempts from immutable inputs.
// It owns no connectors, network operations, clocks, randomness, or mutable health state.
package routing

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrNoCandidate reports that policy rejected every requested route.
	ErrNoCandidate = errors.New("no route candidate satisfies the request")
	// ErrInvalidRequest reports invalid route-planning input.
	ErrInvalidRequest = errors.New("invalid route-planning request")
	// ErrInvalidSnapshot reports invalid or generation-inconsistent candidates.
	ErrInvalidSnapshot = errors.New("invalid route-planning snapshot")
	// ErrInvalidPlan reports incomplete or duplicate attempt identities.
	ErrInvalidPlan = errors.New("invalid route plan")
	// ErrModalityUnsupported reports that the request carries a modality no
	// route accepts. It wraps beside ErrNoCandidate so a caller that answers
	// every routing failure with a 503 can still separate this one, which is
	// a caller mistake and answers 400.
	ErrModalityUnsupported = errors.New("model does not accept a requested modality")
	// ErrOperationUnsupported reports that every offering the request reached
	// serves other operations. A chat model asked to rerank is the case it
	// exists for: without it the planner reports "no candidate", the caller
	// hears a gateway problem, and the real cause stays inside the plan. It
	// wraps beside ErrNoCandidate so a caller answering every routing failure
	// with a 503 can still separate this one, which no retry changes.
	ErrOperationUnsupported = errors.New("model does not serve the requested operation")
)

// Route identifies one provider offering in one immutable catalog generation.
type Route struct {
	CatalogGenerationID string
	ModelID             string
	ProviderID          string
	ProviderModelID     string
	Operation           Operation
	Endpoint            Endpoint
	PromptCacheKnown    bool
	PromptCache         bool

	// MaxDocuments is the longest document list this offering accepts, and
	// zero means the catalog states no bound. It rides on the route rather
	// than on a table beside it, because the bound belongs to the offering the
	// planner chose and differs between two offerings of one model.
	MaxDocuments int
}

// ID returns Starport's provider-scoped route ID.
func (r Route) ID() string {
	return r.ProviderID + "/" + r.ProviderModelID
}

// TokenCost contains provider prices per input and output token.
type TokenCost struct {
	InputPerToken  float64
	OutputPerToken float64
}

// Endpoint is the exact offering endpoint and wire protocol selected for an attempt.
type Endpoint struct {
	Protocol  string
	URL       string
	StreamURL string
}

// Candidate contains facts and runtime measurements for one route. The
// planner does not change this value.
type Candidate struct {
	Route        Route
	Operations   []Operation
	Endpoints    map[Operation]Endpoint
	PromptCache  *bool
	Capabilities []string

	// InputModalities lists what the model reads. An empty list means the
	// catalog states nothing, which the planner reads as silence rather than
	// as a refusal.
	InputModalities []Modality

	ContextWindow int

	// MaxDocuments is the longest document list this offering accepts. The
	// planner reads it nowhere: it carries the fact to the chosen route, which
	// is where the operation that has documents can enforce it.
	MaxDocuments int

	Cost        *TokenCost
	Latency     *time.Duration
	Unavailable bool
	Unhealthy   bool
}

// ServesOperation reports whether the candidate declares the operation. An
// empty operation means the caller named none, which every candidate serves.
func (c Candidate) ServesOperation(operation Operation) bool {
	if operation == "" {
		return true
	}
	for _, declared := range c.Operations {
		if declared == operation {
			return true
		}
	}
	return false
}

// Snapshot binds all planning candidates to one catalog generation and one
// runtime availability revision.
type Snapshot struct {
	CatalogGenerationID  string
	AvailabilityRevision uint64
	Candidates           []Candidate
}

// ProviderAccess grants the account one provider, and optionally narrows
// which of its models. An empty Models list grants every model the provider
// serves. The pairing matters: a flat model set cannot say "every model on
// provider A, only one model on provider B" without denying A's other models.
type ProviderAccess struct {
	Provider string
	Models   []string
}

// AccountPolicy defines the caller's hard model and provider boundaries.
type AccountPolicy struct {
	AllowedModels    []string
	AllowedProviders []string
	ModelOverrides   map[string]string
	// Access names the providers this account may reach, each entry
	// optionally narrowed to specific models. A nil or empty list grants
	// every provider and every model, so a policy-free account plans
	// exactly as before the field existed.
	Access []ProviderAccess
}

// ProviderPolicy defines request-scoped provider constraints and order.
type ProviderPolicy struct {
	Order          []string
	Only           []string
	Ignore         []string
	AllowFallbacks bool

	// MaxPromptPricePerToken and MaxCompletionPricePerToken cap the accepted
	// per-token price. Zero means no cap. A capped request rejects routes
	// whose price is unknown: a cap is a promise the planner can only keep
	// with known prices.
	MaxPromptPricePerToken     float64
	MaxCompletionPricePerToken float64
}

// OptimizationPolicy defines deterministic soft preferences.
type OptimizationPolicy struct {
	PreferLowestCost    bool
	PreferLowestLatency bool
}

// Request contains all policy and requirements used by the pure planner.
type Request struct {
	Models                []string
	Operation             Operation
	AllowModelFallbacks   bool
	AllowAnyModelFallback bool
	RequiredCapabilities  []string
	RequiredModalities    []Modality
	RequiredContextTokens int
	EstimatedInputTokens  int
	EstimatedOutputTokens int
	Account               AccountPolicy
	Providers             ProviderPolicy
	AffinityProvider      string
	Optimization          OptimizationPolicy

	// ZeroPriceModels lists requested model IDs that only accept offerings
	// with a known zero token price (the ":free" variant).
	ZeroPriceModels []string
}

// Modality names one payload family a route accepts. Route planning keeps
// its own vocabulary instead of importing the canonical message types,
// because a plan stays a pure function of the values handed to it.
type Modality string

const (
	// ModalityText is written or spoken language as characters.
	ModalityText Modality = "text"
	// ModalityImage is a still picture.
	ModalityImage Modality = "image"
	// ModalityAudio is recorded sound.
	ModalityAudio Modality = "audio"
	// ModalityDocument is a paged document, such as a PDF.
	ModalityDocument Modality = "document"
	// ModalityVideo is moving pictures.
	ModalityVideo Modality = "video"
)

// RejectionCode is a stable reason that excluded one route.
type RejectionCode string

const (
	// RejectionUnavailable means runtime state disabled the offering.
	RejectionUnavailable RejectionCode = "unavailable"
	// RejectionUnhealthy means runtime health disabled the offering.
	RejectionUnhealthy RejectionCode = "unhealthy"
	// RejectionAccountModel means account policy denied the model.
	RejectionAccountModel RejectionCode = "account_model"
	// RejectionAccountProvider means account policy denied the provider.
	RejectionAccountProvider RejectionCode = "account_provider"
	// RejectionProviderPolicy means request provider policy denied the route.
	RejectionProviderPolicy RejectionCode = "provider_policy"
	// RejectionMissingCapability means the route lacks a required capability.
	RejectionMissingCapability RejectionCode = "missing_capability"
	// RejectionMissingModality means the model does not read a modality the
	// request carries.
	RejectionMissingModality RejectionCode = "missing_modality"
	// RejectionMissingOperation means the exact offering or adapter cannot perform the request.
	RejectionMissingOperation RejectionCode = "missing_operation"
	// RejectionMissingEndpoint means the exact offering has no usable operation endpoint.
	RejectionMissingEndpoint RejectionCode = "missing_endpoint"
	// RejectionInsufficientContext means the route cannot accept the required context.
	RejectionInsufficientContext RejectionCode = "insufficient_context"
	// RejectionPriceExceeded means the route price violates a request price cap.
	RejectionPriceExceeded RejectionCode = "price_exceeded"
	// RejectionUnknownModel means a requested model matched no catalog offering.
	// The rejection carries only the model identity, not a full route.
	RejectionUnknownModel RejectionCode = "unknown_model"
)

// Rejection records why one considered route was not planned.
type Rejection struct {
	Route  Route
	Code   RejectionCode
	Detail string
}

// SelectionEvidence records the pure ranks and measurements used to order an attempt.
type SelectionEvidence struct {
	ModelRank        int
	ProviderRank     int
	AffinityMatched  bool
	EstimatedCost    float64
	HasCost          bool
	EstimatedLatency time.Duration
	HasLatency       bool
}

// Attempt is one ordered provider attempt in a route plan.
type Attempt struct {
	Route    Route
	Evidence SelectionEvidence
}

// Plan is an immutable ordered attempt list with rejection evidence.
type Plan struct {
	catalogGenerationID  string
	availabilityRevision uint64
	attempts             []Attempt
	rejections           []Rejection
}

// NewPlan creates an immutable plan from an already ordered attempt set.
// Composition adapters use it when no catalog-backed planner is available.
func NewPlan(
	catalogGenerationID string,
	availabilityRevision uint64,
	attempts []Attempt,
	rejections []Rejection,
) (*Plan, error) {
	if catalogGenerationID == "" {
		return nil, fmt.Errorf("%w: catalog generation ID is required", ErrInvalidPlan)
	}
	seen := make(map[string]struct{}, len(attempts))
	for index, attempt := range attempts {
		route := attempt.Route
		if route.CatalogGenerationID != catalogGenerationID || route.ModelID == "" || route.ProviderID == "" || route.ProviderModelID == "" {
			return nil, fmt.Errorf("%w: attempt %d has incomplete route identity", ErrInvalidPlan, index)
		}
		if route.Operation != "" && (route.Endpoint.Protocol == "" || route.Endpoint.URL == "") {
			return nil, fmt.Errorf("%w: attempt %d has incomplete endpoint", ErrInvalidPlan, index)
		}
		if _, exists := seen[route.ID()]; exists {
			return nil, fmt.Errorf("%w: duplicate route %q", ErrInvalidPlan, route.ID())
		}
		seen[route.ID()] = struct{}{}
	}
	return &Plan{
		catalogGenerationID:  catalogGenerationID,
		availabilityRevision: availabilityRevision,
		attempts:             append([]Attempt(nil), attempts...),
		rejections:           append([]Rejection(nil), rejections...),
	}, nil
}

// CatalogGenerationID returns the generation that supplied every planned route.
func (p *Plan) CatalogGenerationID() string {
	if p == nil {
		return ""
	}
	return p.catalogGenerationID
}

// AvailabilityRevision returns the runtime revision used for this plan.
func (p *Plan) AvailabilityRevision() uint64 {
	if p == nil {
		return 0
	}
	return p.availabilityRevision
}

// Attempts returns a caller-owned copy in execution order.
func (p *Plan) Attempts() []Attempt {
	if p == nil {
		return nil
	}
	return append([]Attempt(nil), p.attempts...)
}

// Rejections returns a caller-owned copy in stable route order.
func (p *Plan) Rejections() []Rejection {
	if p == nil {
		return nil
	}
	return append([]Rejection(nil), p.rejections...)
}
