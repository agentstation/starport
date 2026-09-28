package connectors

import "encoding/json/v2"

// UnmarshalJSON preserves every input component needed to compute the prompt total.
func (u *anthropicUsage) UnmarshalJSON(data []byte) error {
	var wire struct {
		Input  *int `json:"input_tokens"`
		Output *int `json:"output_tokens"`
		Read   *int `json:"cache_read_input_tokens"`
		Write  *int `json:"cache_creation_input_tokens"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*u = anthropicUsage{decoded: true, promptReported: wire.Input != nil && wire.Read != nil && wire.Write != nil, outputReported: wire.Output != nil}
	if wire.Input != nil {
		u.InputTokens = *wire.Input
	}
	if wire.Output != nil {
		u.OutputTokens = *wire.Output
	}
	if wire.Read != nil {
		u.CacheReadInputTokens = *wire.Read
	}
	if wire.Write != nil {
		u.CacheCreationInputTokens = *wire.Write
	}
	return nil
}
