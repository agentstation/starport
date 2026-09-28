package connectors

import (
	"encoding/json/v2"
	"math"
)

const (
	usagePromptReported uint8 = 1 << iota
	usageCompletionReported
	usageTotalReported
	usageAllTotalsReported = usagePromptReported | usageCompletionReported | usageTotalReported
)

// HasReportedTotals distinguishes complete token counts from decoded omissions.
// Programmatic usage values supply explicit counts. Wire adapters must preserve
// omissions when they construct normalized usage from another protocol.
func (u Usage) HasReportedTotals() bool {
	return !u.decoded || u.reportedTotals == usageAllTotalsReported
}

func (u *Usage) setReportedTotals(prompt, completion, total bool) {
	u.decoded, u.reportedTotals = true, 0
	if prompt {
		u.reportedTotals |= usagePromptReported
	}
	if completion {
		u.reportedTotals |= usageCompletionReported
	}
	if total {
		u.reportedTotals |= usageTotalReported
	}
}

type usageWire struct {
	Prompt     *int                     `json:"prompt_tokens,omitempty"`
	Completion *int                     `json:"completion_tokens,omitempty"`
	Total      *int                     `json:"total_tokens,omitempty"`
	Input      *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	Output     *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// UnmarshalJSON retains absent and null counts independently of their values.
func (u *Usage) UnmarshalJSON(data []byte) error {
	var wire usageWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*u = Usage{PromptTokensDetails: wire.Input, CompletionTokensDetails: wire.Output}
	u.setReportedTotals(wire.Prompt != nil, wire.Completion != nil, wire.Total != nil)
	if wire.Prompt != nil {
		u.PromptTokens = *wire.Prompt
	}
	if wire.Completion != nil {
		u.CompletionTokens = *wire.Completion
	}
	if wire.Total != nil {
		u.TotalTokens = *wire.Total
	}
	return nil
}

// MarshalJSON preserves missing counts and explicit zeros across serialization.
func (u Usage) MarshalJSON() ([]byte, error) {
	wire := usageWire{Input: u.PromptTokensDetails, Output: u.CompletionTokensDetails}
	if !u.decoded || u.reportedTotals&usagePromptReported != 0 {
		wire.Prompt = &u.PromptTokens
	}
	if !u.decoded || u.reportedTotals&usageCompletionReported != 0 {
		wire.Completion = &u.CompletionTokens
	}
	if !u.decoded || u.reportedTotals&usageTotalReported != 0 {
		wire.Total = &u.TotalTokens
	}
	return json.Marshal(wire)
}

// sumUsageTokens rejects invalid source components before normalizing totals.
func sumUsageTokens(values ...int) (int, bool) {
	total := 0
	for _, value := range values {
		if value < 0 || value > math.MaxInt-total {
			return 0, false
		}
		total += value
	}
	return total, true
}
