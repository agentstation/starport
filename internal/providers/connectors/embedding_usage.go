package connectors

import "encoding/json/v2"

// UnmarshalJSON preserves a missing usage record as unknown.
func (r *EmbeddingsResponse) UnmarshalJSON(data []byte) error {
	type plain EmbeddingsResponse
	var decoded plain
	decoded.Usage.setReportedTotals(false, false, false)
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = EmbeddingsResponse(decoded)
	return nil
}

// ReportedInputTokens returns complete measured embedding usage.
// Missing, estimated, negative, or inconsistent counts cannot settle a budget.
func (r *EmbeddingsResponse) ReportedInputTokens() (int64, bool) {
	if r == nil {
		return 0, false
	}
	u := r.Usage
	const required = usagePromptReported | usageTotalReported
	if u.decoded && u.reportedTotals&required != required || !u.decoded && u.PromptTokens == 0 && u.TotalTokens == 0 {
		return 0, false
	}
	if u.PromptTokens < 0 || u.PromptTokens != u.TotalTokens || u.CompletionTokens != 0 || u.CacheWriteTokens != 0 {
		return 0, false
	}
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.AudioTokens != 0 || u.CompletionTokensDetails != nil && u.CompletionTokensDetails.AudioTokens != 0 {
		return 0, false
	}
	return int64(u.PromptTokens), true
}
