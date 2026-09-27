package router

import (
	"context"
	"errors"
	"math"

	"github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

type billingPurpose string

const (
	billingVideoPoll    billingPurpose = "videos-poll"
	billingVideoCancel  billingPurpose = "videos-cancel"
	billingVideoContent billingPurpose = "videos-content"
)

// WithBudgetAdmission installs the application-owned admission boundary.
// A nil owner still checks policy and refuses every required budget.
func WithBudgetAdmission(owner *admission.Owner) Option {
	return func(r *modelRouter) { r.budget, r.checkBudgets = owner, true }
}

func (r *modelRouter) admit(ctx context.Context, account string, route routing.Route, operation string, quote admission.QuoteFunc) (admission.Ticket, *failure.Failure) {
	// Isolated routing compositions can omit accounting. Production composition
	// always installs this boundary before serving any request.
	if !r.checkBudgets {
		return admission.Ticket{}, nil
	}
	ticket, err := r.budget.Start(ctx, admission.Target{
		AccountID: account, OfferingID: route.ID(), CatalogGeneration: route.CatalogGenerationID,
		Operation: operation,
	}, quote)
	if err != nil {
		return admission.Ticket{}, budgetFailure(err)
	}
	return ticket, nil
}

func budgetFailure(err error) *failure.Failure {
	kind, message := failure.GatewayUnavailable, "Required budget admission is unavailable."
	if errors.Is(err, reservation.ErrExhausted) {
		kind, message = failure.Quota, "The request exceeds available budget capacity."
	} else if errors.Is(err, admission.ErrBoundUnknown) {
		message = "The selected offering has no verified billing bound for this request."
	}
	return failure.New(kind, message, false, failure.ProviderDetails{}, err)
}

// chatTokenQuote uses provider limits, never a local token estimate. This first
// projection supports token-only accounting. Spend requires a complete billing
// projection, including every applicable unit, before it can authorize dispatch.
func chatTokenQuote(snapshot *runtimecatalog.RoutableSnapshot, route routing.Route, request *connectors.ChatRequest) admission.QuoteFunc {
	return func(needed admission.Requirements) (admission.Quote, error) {
		if needed.Spend || snapshot == nil || snapshot.GenerationID() != route.CatalogGenerationID || request == nil || len(request.ProviderOptions) != 0 {
			return admission.Quote{}, admission.ErrBoundUnknown
		}
		offering, err := snapshot.Offering(runtimecatalog.Route{ProviderID: catalogs.ProviderID(route.ProviderID), ProviderModelID: catalogs.ProviderModelID(route.ProviderModelID)})
		if err != nil || offering.Limits == nil {
			return admission.Quote{}, admission.ErrBoundUnknown
		}
		count := int64(1)
		if request.N != nil {
			count = int64(*request.N)
		}
		bound, err := declaredChatTokenBound(offering.Limits, count)
		if err != nil {
			return admission.Quote{}, err
		}
		return admission.Quote{Tokens: &bound}, nil
	}
}

func declaredChatTokenBound(limits *catalogs.ModelLimits, count int64) (int64, error) {
	input, inputKnown := limits.Value(catalogs.ModelLimitInputTokens)
	output, outputKnown := limits.Value(catalogs.ModelLimitOutputTokens)
	if inputKnown != catalogs.ValueKnown || input <= 0 {
		// A declared context window bounds the entire prompt, even when the
		// offering supplies no separate positive input-token limit.
		input, inputKnown = limits.Value(catalogs.ModelLimitContextWindow)
	}
	if inputKnown != catalogs.ValueKnown || outputKnown != catalogs.ValueKnown || input <= 0 || output <= 0 ||
		count <= 0 || input > math.MaxInt64-output || input+output > math.MaxInt64/count {
		return 0, admission.ErrBoundUnknown
	}
	// Reserve input and output for every candidate. This can exceed the
	// provider's actual input charge, but cannot undercount repeated input.
	return (input + output) * count, nil
}

func finishChatBudget(ctx context.Context, ticket admission.Ticket, usage *connectors.Usage) error {
	if ticket.ID() == "" {
		return nil
	}
	var evidence *reservation.Evidence
	if usage != nil && usage.PromptTokens >= 0 && usage.CompletionTokens >= 0 && usage.TotalTokens >= 0 &&
		usage.PromptTokens <= math.MaxInt-usage.CompletionTokens && usage.TotalTokens == usage.PromptTokens+usage.CompletionTokens {
		evidence = &reservation.Evidence{ID: ticket.ID() + ":usage", Tokens: int64(usage.TotalTokens)}
	}
	return ticket.Finish(ctx, evidence)
}
