package router

import (
	"context"
	"crypto/rand"
	"errors"
	"math"
	"slices"
	"time"

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

func (r *modelRouter) admit(ctx context.Context, requestID, account string, route routing.Route, operation string, quote admission.QuoteFunc) (admission.Ticket, *failure.Failure) {
	// Isolated routing compositions can omit accounting. Production composition
	// always installs this boundary before serving any request.
	if !r.checkBudgets {
		return admission.Ticket{}, nil
	}
	ticket, err := r.budget.Start(ctx, admission.Target{
		RequestID: requestID, AccountID: account, OfferingID: route.ID(), CatalogGeneration: route.CatalogGenerationID,
		Operation: operation,
	}, quote)
	if err != nil {
		return admission.Ticket{}, budgetFailure(err)
	}
	return ticket, nil
}

// budgetRequestID preserves correlation across retries. A direct router caller
// without an identity gets one ID for this invocation, separate from attempt IDs.
func budgetRequestID(id string) string {
	if id == "" {
		return rand.Text()
	}
	return id
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

// chatTokenQuote uses provider limits, never a local token estimate.
// It retains the exact billing declaration for settlement after catalog refresh.
func chatTokenQuote(snapshot *runtimecatalog.RoutableSnapshot, route routing.Route, request *connectors.ChatRequest, retained **catalogs.TextChatBilling) admission.QuoteFunc {
	return func(needed admission.Requirements) (admission.Quote, error) {
		if snapshot == nil || snapshot.GenerationID() != route.CatalogGenerationID || request == nil || len(request.ProviderOptions) != 0 {
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
		quote := admission.Quote{Tokens: &bound}
		if needed.Spend {
			if !textChatBillingScope(request) {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			valuation, err := runtimecatalog.TextChatValuation(offering, time.Now())
			if err != nil {
				return admission.Quote{}, admission.ErrBoundUnknown
			}
			quote.Valuation = valuation
			quote.Units = make(reservation.Quantities, len(valuation.Components))
			input, _ := offering.Limits.Value(catalogs.ModelLimitInputTokens)
			if input <= 0 {
				input, _ = offering.Limits.Value(catalogs.ModelLimitContextWindow)
			}
			output, _ := offering.Limits.Value(catalogs.ModelLimitOutputTokens)
			for _, class := range offering.Billing.TextChat.Input {
				quote.Units[string(class)] = input * count
			}
			for _, class := range offering.Billing.TextChat.Output {
				quote.Units[string(class)] = output * count
			}
			if *offering.Billing.TextChat.RequestCharge {
				quote.Units["request"] = 1
			}
			*retained = offering.Billing.TextChat
		}
		return quote, nil
	}
}

func textChatBillingScope(request *connectors.ChatRequest) bool {
	if len(request.ProviderOptions) != 0 || request.Audio != nil || len(request.Models) != 0 {
		return false
	}
	for _, modality := range request.Modalities {
		if modality != "text" {
			return false
		}
	}
	for _, tool := range request.Tools {
		if tool.Type != "function" {
			return false
		}
	}
	for _, message := range request.Messages {
		if message.Audio != nil || len(message.Images) != 0 {
			return false
		}
		switch content := message.Content.(type) {
		case nil, string:
		case []connectors.ContentPart:
			for _, part := range content {
				if part.Type != "text" || part.ImageURL != nil || part.InputAudio != nil || part.File != nil || part.VideoURL != nil || part.CacheControl != nil {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
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

func finishChatBudget(ctx context.Context, ticket admission.Ticket, usage *connectors.Usage, billing *catalogs.TextChatBilling) error {
	if ticket.ID() == "" {
		return nil
	}
	var evidence *reservation.Evidence
	if usage != nil && usage.PromptTokens >= 0 && usage.CompletionTokens >= 0 && usage.TotalTokens >= 0 &&
		usage.PromptTokens <= math.MaxInt-usage.CompletionTokens && usage.TotalTokens == usage.PromptTokens+usage.CompletionTokens {
		evidence = &reservation.Evidence{ID: ticket.ID() + ":usage", Tokens: int64(usage.TotalTokens)}
		if billing != nil {
			quantities, known := textChatQuantities(billing, usage)
			if !known {
				evidence = nil
			} else {
				evidence.Quantities = quantities
			}
		}
	}
	return ticket.Finish(ctx, evidence)
}

func textChatQuantities(billing *catalogs.TextChatBilling, usage *connectors.Usage) (reservation.Quantities, bool) {
	input, output := int64(usage.PromptTokens), int64(usage.CompletionTokens)
	quantities := reservation.Quantities{}
	if usage.PromptTokensDetails != nil && usage.PromptTokensDetails.AudioTokens != 0 || usage.CompletionTokensDetails != nil && usage.CompletionTokensDetails.AudioTokens != 0 {
		return nil, false
	}
	for _, class := range billing.Input {
		switch class {
		case catalogs.TokenBillingInput:
		case catalogs.TokenBillingCacheRead:
			count, known := usage.PromptTokensDetails.ReportedCachedTokens()
			if !known || count < 0 || int64(count) > input {
				return nil, false
			}
			quantities[string(class)] = int64(count)
			input -= int64(count)
		case catalogs.TokenBillingCacheWrite:
			// The existing normalized field has no absence marker. It cannot
			// establish a complete cache-write count for strict settlement.
			return nil, false
		default:
			return nil, false
		}
	}
	if !slices.Contains(billing.Input, catalogs.TokenBillingCacheWrite) && usage.CacheWriteTokens != 0 {
		return nil, false
	}
	for _, class := range billing.Output {
		switch class {
		case catalogs.TokenBillingOutput:
		case catalogs.TokenBillingReasoning:
			count, known := usage.CompletionTokensDetails.ReportedReasoningTokens()
			if !known || count < 0 || int64(count) > output {
				return nil, false
			}
			quantities[string(class)] = int64(count)
			output -= int64(count)
		default:
			return nil, false
		}
	}
	quantities[string(catalogs.TokenBillingInput)] = input
	quantities[string(catalogs.TokenBillingOutput)] = output
	if *billing.RequestCharge {
		quantities["request"] = 1
	}
	return quantities, true
}
