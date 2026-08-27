package cache

import (
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

// A cached answer is stored once and replayed to every later caller, including
// the ones that asked for a stream. Replay used to refuse any part that was not
// text, so a caller who asked for a picture received it only while the cache
// missed, and received an error once it hit. These tests hold the repair.

func mediaAnswer() inference.ChatResponse {
	return inference.ChatResponse{
		ID:    "chatcmpl-1",
		Model: "openai/gpt-image-1",
		Choices: []inference.Choice{{
			Index: 0,
			Message: inference.Message{
				Role: inference.RoleAssistant,
				Content: []inference.ContentPart{
					{Kind: inference.ContentText, Text: "here it is"},
					{Kind: inference.ContentImage, Image: &inference.Image{
						URL: "data:image/png;base64,AAAA", Detail: "high",
					}},
				},
			},
			FinishReason: "stop",
		}},
	}
}

// TestReplayCarriesAGeneratedImage is the acceptance case. The image has to
// reach the delta, because that is the only event a streaming caller reads.
func TestReplayCarriesAGeneratedImage(t *testing.T) {
	events, err := StreamEvents(mediaAnswer(), inference.StreamOptions{})
	require.NoError(t, err)

	var media []inference.ContentPart
	var text string
	for _, event := range events {
		for _, delta := range event.Deltas {
			media = append(media, delta.Media...)
			text += delta.Text
		}
	}
	require.Equal(t, "here it is", text)
	require.Len(t, media, 1, "the replayed stream dropped the picture")
	require.Equal(t, inference.ContentImage, media[0].Kind)
	require.Equal(t, "data:image/png;base64,AAAA", media[0].Image.URL)
}

// TestReplayRoundTripsBackToTheStoredAnswer holds the pair of functions
// together. Replay and completion are inverses, and a caller that streams a
// cache hit has to end up with what a caller that buffered it received.
func TestReplayRoundTripsBackToTheStoredAnswer(t *testing.T) {
	stored := mediaAnswer()
	events, err := StreamEvents(stored, inference.StreamOptions{})
	require.NoError(t, err)

	rebuilt, err := CompleteStream(events)
	require.NoError(t, err)
	require.Equal(t, stored.Choices[0].Message.Content, rebuilt.Choices[0].Message.Content)
	require.Equal(t, stored.Choices[0].FinishReason, rebuilt.Choices[0].FinishReason)
}

// TestReplayRefusesAMalformedPart states the one case replay still cannot
// resolve. A part that claims to be text while carrying a picture is not a
// part a reader can interpret without guessing which half to believe.
func TestReplayRefusesAMalformedPart(t *testing.T) {
	_, err := StreamEvents(inference.ChatResponse{
		Choices: []inference.Choice{{
			Message: inference.Message{
				Role: inference.RoleAssistant,
				Content: []inference.ContentPart{{
					Kind:  inference.ContentText,
					Text:  "here it is",
					Image: &inference.Image{URL: "data:image/png;base64,AAAA"},
				}},
			},
		}},
	}, inference.StreamOptions{})
	require.True(t, errors.Is(err, ErrUnsupportedContent))
}

// TestTextArrivesInItsOwnPartAfterMedia holds the accumulation order. A
// provider sends the finished picture in one delta, and the words may follow
// it. Completion used to write streamed text into the first content part
// whatever that part was, so the answer's words landed inside the image.
func TestTextArrivesInItsOwnPartAfterMedia(t *testing.T) {
	response, err := CompleteStream([]inference.StreamEvent{
		{Kind: inference.StreamDelta, Deltas: []inference.ChoiceDelta{{
			Index: 0,
			Role:  inference.RoleAssistant,
			Media: []inference.ContentPart{{
				Kind:  inference.ContentImage,
				Image: &inference.Image{URL: "data:image/png;base64,AAAA"},
			}},
		}}},
		{Kind: inference.StreamDelta, Deltas: []inference.ChoiceDelta{{
			Index: 0, Text: "here ",
		}}},
		{Kind: inference.StreamDelta, Deltas: []inference.ChoiceDelta{{
			Index: 0, Text: "it is", FinishReason: "stop",
		}}},
	})
	require.NoError(t, err)

	parts := response.Choices[0].Message.Content
	require.Len(t, parts, 2)
	require.Equal(t, inference.ContentImage, parts[0].Kind)
	require.Equal(t, "", parts[0].Text, "the answer's words were written into the image part")
	require.Equal(t, inference.ContentText, parts[1].Kind)
	require.Equal(t, "here it is", parts[1].Text)
}
