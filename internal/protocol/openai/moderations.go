package openai

import (
	"encoding/json"
	"io"

	"github.com/agentstation/starport/internal/inference"
)

// OpenAI publishes /v1/moderations, and it is the one moderation route a
// 2026 SDK expects, so the gateway serves its wire shape. The request's
// input field is polymorphic on that wire: one string, or a list of strings,
// or a list of typed parts that can carry images. The gateway decodes the
// two text forms and refuses the typed parts by name, because a silently
// dropped image would return a verdict on less than the caller sent.

// ModerationRequest is the moderation wire request served at POST
// /v1/moderations. Input stays raw until decode, because its type decides
// its meaning.
type ModerationRequest struct {
	Model string          `json:"model"`
	Input json.RawMessage `json:"input"`
}

// ModerationResult is one input's verdict on the wire. The two maps repeat
// the wire convention this route's callers parse: a threshold decision per
// category under categories, and the score behind it under category_scores.
type ModerationResult struct {
	Flagged        bool               `json:"flagged"`
	Categories     map[string]bool    `json:"categories"`
	CategoryScores map[string]float64 `json:"category_scores"`
}

// ModerationResponse is the moderation wire response.
type ModerationResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model"`
	Results []ModerationResult `json:"results"`
}

// DecodeModerations decodes one strict moderation request. An unknown field
// fails the same way it fails on the chat route.
func DecodeModerations(reader io.Reader) (inference.ModerationRequest, error) {
	var wire ModerationRequest
	if err := decodeStrict(reader, &wire); err != nil {
		return inference.ModerationRequest{}, err
	}
	inputs, err := decodeModerationInput(wire.Input)
	if err != nil {
		return inference.ModerationRequest{}, err
	}
	return inference.NewModerationRequest(wire.Model, inputs)
}

// decodeModerationInput reads the polymorphic input field. A single string
// is a one-item list, a list of strings is itself, and anything else is
// refused by name.
func decodeModerationInput(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, inference.ErrModerationInputEmpty
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}
	return nil, &UnsupportedError{
		Param:   "input",
		Message: "input must be a string or a list of strings; typed input parts are not supported",
	}
}

// EncodeModerations writes one canonical moderation answer. It validates
// against the request first, because a result list that answers a different
// number of inputs reads as ordinary and shifts every verdict onto the
// wrong input.
func EncodeModerations(
	response inference.ModerationResponse,
	request inference.ModerationRequest,
) (ModerationResponse, error) {
	if err := response.Validate(request); err != nil {
		return ModerationResponse{}, err
	}
	results := make([]ModerationResult, len(response.Results))
	for index, result := range response.Results {
		flags := make(map[string]bool, len(result.Categories))
		scores := make(map[string]float64, len(result.Categories))
		for _, category := range result.Categories {
			flags[category.Name] = category.Flagged
			scores[category.Name] = category.Score
		}
		results[index] = ModerationResult{
			Flagged:        result.Flagged,
			Categories:     flags,
			CategoryScores: scores,
		}
	}
	return ModerationResponse{
		ID:      response.ID,
		Model:   responseModel(response.Model, request.Model),
		Results: results,
	}, nil
}
