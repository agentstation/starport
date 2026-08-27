package connectors

import (
	"encoding/base64"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

// A model that answers with a picture or a voice needs two things from this
// adapter. The request has to reach the provider still asking for that answer,
// and the answer has to reach the canonical types still holding its payload.
// Either half alone produces a turn that looks successful and returns nothing:
// a dropped request field makes the provider answer in text, and a dropped
// response field makes the gateway hand the caller an empty message.

// TestOutputModalityRoundTripsThroughTheAdapter holds the request half. The
// wire spellings below are the ones every OpenAI-compatible provider reads, so
// a rename here is a change the provider sees.
func TestOutputModalityRoundTripsThroughTheAdapter(t *testing.T) {
	canonical := inference.ChatRequest{
		Model: "openai/gpt-4o-audio-preview",
		Messages: []inference.Message{{
			Role:    inference.RoleUser,
			Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "say hello"}},
		}},
		OutputModalities: []inference.Modality{inference.ModalityText, inference.ModalityAudio},
		AudioOutput:      &inference.AudioOutput{Voice: "alloy", Format: "wav"},
	}

	wire, err := ChatRequestFromInference(canonical)
	require.NoError(t, err)
	require.Equal(t, []string{"text", "audio"}, wire.Modalities)
	require.NotNil(t, wire.Audio, "the provider never learns which voice to use")
	require.Equal(t, "alloy", wire.Audio.Voice)
	require.Equal(t, "wav", wire.Audio.Format)

	back, err := ChatRequestToInference(wire)
	require.NoError(t, err)
	require.Equal(t, canonical.OutputModalities, back.OutputModalities)
	require.Equal(t, canonical.AudioOutput, back.AudioOutput)
}

// TestAskingForNothingSendsNoModalityField holds the other side of the request
// half. Every ordinary text turn passes through here, and a field that appears
// on all of them changes what a provider stores and what it bills.
func TestAskingForNothingSendsNoModalityField(t *testing.T) {
	wire, err := ChatRequestFromInference(inference.ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []inference.Message{{
			Role:    inference.RoleUser,
			Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}},
		}},
	})
	require.NoError(t, err)
	require.Nil(t, wire.Modalities)
	require.Nil(t, wire.Audio)
}

// TestGeneratedImageReachesTheCanonicalMessage holds the response half for a
// picture. A provider returns a generated image beside the content rather than
// inside it, so a reader that walks content alone finds an empty message.
func TestGeneratedImageReachesTheCanonicalMessage(t *testing.T) {
	response, err := ChatResponseToInference(&ChatResponse{
		ID:    "chatcmpl-1",
		Model: "openai/gpt-image-1",
		Choices: []Choice{{
			Index: 0,
			Message: Message{
				Role:    RoleAssistant,
				Content: "here it is",
				Images: []GeneratedImage{{
					Type:     contentTypeImageURL,
					ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"},
				}},
			},
			FinishReason: finishReasonStop,
		}},
	}, "openai/gpt-image-1")
	require.NoError(t, err)

	parts := response.Choices[0].Message.Content
	require.Len(t, parts, 2, "the answer keeps its words and its picture")
	require.Equal(t, inference.ContentText, parts[0].Kind)
	require.Equal(t, "here it is", parts[0].Text)
	require.Equal(t, inference.ContentImage, parts[1].Kind)
	require.Equal(t, "data:image/png;base64,AAAA", parts[1].Image.URL)
}

// TestGeneratedAudioReachesTheCanonicalMessage holds the response half for a
// spoken answer. A caller that cannot play audio still needs the transcript,
// so the answer arrives as two parts rather than as bytes alone.
func TestGeneratedAudioReachesTheCanonicalMessage(t *testing.T) {
	spoken := []byte{0x52, 0x49, 0x46, 0x46}
	response, err := ChatResponseToInference(&ChatResponse{
		ID:    "chatcmpl-2",
		Model: "openai/gpt-4o-audio-preview",
		Choices: []Choice{{
			Index: 0,
			Message: Message{
				Role: RoleAssistant,
				Audio: &GeneratedAudio{
					Data:       base64.StdEncoding.EncodeToString(spoken),
					Transcript: "hello there",
					Format:     "wav",
				},
			},
			FinishReason: finishReasonStop,
		}},
	}, "openai/gpt-4o-audio-preview")
	require.NoError(t, err)

	parts := response.Choices[0].Message.Content
	require.Len(t, parts, 2)
	require.Equal(t, inference.ContentText, parts[0].Kind)
	require.Equal(t, "hello there", parts[0].Text)
	require.Equal(t, inference.ContentAudio, parts[1].Kind)
	require.Equal(t, spoken, parts[1].Audio.Data)
	require.Equal(t, "wav", parts[1].Audio.Format)
}

// TestUndecodableGeneratedAudioFails states the failure a silent decode would
// hide. Audio arrives as base64 with no data URL prefix, and bytes that do not
// decode are not an empty answer, they are a broken one.
func TestUndecodableGeneratedAudioFails(t *testing.T) {
	_, err := ChatResponseToInference(&ChatResponse{
		Choices: []Choice{{
			Message: Message{Role: RoleAssistant, Audio: &GeneratedAudio{Data: "not base64!!"}},
		}},
	}, "openai/gpt-4o-audio-preview")
	require.Error(t, err)
}

// TestStreamedAudioReachesTheDelta holds the streaming half. OpenRouter serves
// generated audio through streaming alone, so a delta that drops the chunk
// drops the entire answer for that route.
func TestStreamedAudioReachesTheDelta(t *testing.T) {
	spoken := []byte{0x01, 0x02, 0x03}
	events, err := StreamEventsToInference(&ChatStreamChunk{
		ID:    "chatcmpl-3",
		Model: "openai/gpt-4o-audio-preview",
		Choices: []StreamChoice{{
			Index: 0,
			Delta: MessageDelta{
				Audio: &GeneratedAudio{
					Data:       base64.StdEncoding.EncodeToString(spoken),
					Transcript: "hel",
				},
			},
		}},
	}, "openai/gpt-4o-audio-preview")
	require.NoError(t, err)
	require.Len(t, events, 1)

	delta := events[0].Deltas[0]
	require.NotNil(t, delta.Audio)
	require.Equal(t, spoken, delta.Audio.Data)
	require.Equal(t, "hel", delta.Audio.Transcript)
}

// TestAChunkCarryingMediaIsNotAStartEvent holds the classification. A start
// event announces the turn and carries nothing, so a consumer is free to skip
// its deltas. A provider that sends the finished picture together with the
// assistant role would have produced exactly that shape.
func TestAChunkCarryingMediaIsNotAStartEvent(t *testing.T) {
	events, err := StreamEventsToInference(&ChatStreamChunk{
		ID: "chatcmpl-4",
		Choices: []StreamChoice{{
			Index: 0,
			Delta: MessageDelta{
				Role: RoleAssistant,
				Images: []GeneratedImage{{
					Type:     contentTypeImageURL,
					ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"},
				}},
			},
		}},
	}, "openai/gpt-image-1")
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotEqual(t, inference.StreamStart, events[0].Kind,
		"the picture arrived on an event a consumer may discard")
	require.Len(t, events[0].Deltas[0].Media, 1)
}
