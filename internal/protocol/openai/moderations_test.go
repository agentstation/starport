package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
)

func moderationAnswer() inference.ModerationResponse {
	return inference.ModerationResponse{
		ID:    "modr-1",
		Model: "omni-moderation-2024-09-26",
		Results: []inference.ModerationResult{
			{
				Flagged: true,
				Categories: []inference.ModerationCategory{
					{Name: "harassment", Flagged: false, Score: 0.02},
					{Name: "violence", Flagged: true, Score: 0.94},
				},
			},
		},
	}
}

// TestTheModerationCodecRoundTripsThePublishedShape holds condition ENR-V15's
// wire half. A caller written against OpenAI sends input as a bare string or
// a list, and reads categories and category_scores off each result, so both
// maps have to carry every category by its provider name.
func TestTheModerationCodecRoundTripsThePublishedShape(t *testing.T) {
	t.Parallel()

	decoded, err := DecodeModerations(strings.NewReader(`{
	  "model": "omni-moderation-latest",
	  "input": ["I want to hurt someone."]
	}`))
	require.NoError(t, err)
	require.Equal(t, "omni-moderation-latest", decoded.Model)
	require.Equal(t, []string{"I want to hurt someone."}, decoded.Inputs)

	encoded, err := EncodeModerations(moderationAnswer(), decoded)
	require.NoError(t, err)

	written, err := json.Marshal(encoded)
	require.NoError(t, err)
	require.JSONEq(t, `{
	  "id": "modr-1",
	  "model": "omni-moderation-2024-09-26",
	  "results": [{
	    "flagged": true,
	    "categories": {"harassment": false, "violence": true},
	    "category_scores": {"harassment": 0.02, "violence": 0.94}
	  }]
	}`, string(written))
}

// TestASingleStringInputIsAOneItemList holds the polymorphic decode. The
// published request takes one string or a list, and both have to land on the
// same canonical shape.
func TestASingleStringInputIsAOneItemList(t *testing.T) {
	t.Parallel()

	decoded, err := DecodeModerations(strings.NewReader(`{
	  "model": "omni-moderation-latest",
	  "input": "one text"
	}`))
	require.NoError(t, err)
	require.Equal(t, []string{"one text"}, decoded.Inputs)
}

// TestTypedInputPartsAreRefusedByName guards the refusal this codec chose over
// a silent drop. A typed part can carry an image, and a verdict on less than
// the caller sent reads as a verdict on all of it.
func TestTypedInputPartsAreRefusedByName(t *testing.T) {
	t.Parallel()

	_, err := DecodeModerations(strings.NewReader(`{
	  "model": "omni-moderation-latest",
	  "input": [{"type": "text", "text": "hello"}]
	}`))
	var unsupported *UnsupportedError
	require.ErrorAs(t, err, &unsupported)
	require.Equal(t, "input", unsupported.Param)
}

// TestModerationDecodeRefusesTheUnanswerable keeps the empty request and the
// misspelled one out of the router.
func TestModerationDecodeRefusesTheUnanswerable(t *testing.T) {
	t.Parallel()

	_, err := DecodeModerations(strings.NewReader(`{"model": "omni-moderation-latest"}`))
	require.ErrorIs(t, err, inference.ErrModerationInputEmpty)

	_, err = DecodeModerations(strings.NewReader(`{
	  "model": "omni-moderation-latest",
	  "input": "x",
	  "inputs": "x"
	}`))
	require.Error(t, err, "an unknown field fails the same way it fails on the chat route")
}

// TestModerationEncodeRefusesAMisshapenAnswer holds the validation gate in
// front of the wire. A result list that answers a different number of inputs
// would read as ordinary JSON.
func TestModerationEncodeRefusesAMisshapenAnswer(t *testing.T) {
	t.Parallel()

	request, err := inference.NewModerationRequest("omni-moderation-latest", []string{"one", "two"})
	require.NoError(t, err)

	_, err = EncodeModerations(moderationAnswer(), request)
	require.ErrorIs(t, err, inference.ErrModerationResultCountMismatch)
}

// TestModerationEncodeFallsBackToTheRequestedModel keeps the model field
// filled when a provider answers without one.
func TestModerationEncodeFallsBackToTheRequestedModel(t *testing.T) {
	t.Parallel()

	request, err := inference.NewModerationRequest("omni-moderation-latest", []string{"one"})
	require.NoError(t, err)
	answer := moderationAnswer()
	answer.Model = ""

	encoded, err := EncodeModerations(answer, request)
	require.NoError(t, err)
	require.Equal(t, "omni-moderation-latest", encoded.Model)
}
