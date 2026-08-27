package openrouter

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

// Phase A taught this codec to carry media a caller sends. This file holds the
// return trip: what a caller writes to ask for a picture or a voice, and what
// it reads back when one arrives. OpenRouter documents the same spellings the
// OpenAI API uses, so the two codecs answer the same shape and a caller that
// switches routes changes nothing but the path.

// TestDecodeOutputModalityRequest holds the request spelling.
func TestDecodeOutputModalityRequest(t *testing.T) {
	body := `{"model":"openai/gpt-4o-audio-preview",` +
		`"modalities":["text","audio"],` +
		`"audio":{"voice":"alloy","format":"wav"},` +
		`"messages":[{"role":"user","content":"say hello"}]}`

	decoded, err := DecodeChat(strings.NewReader(body))
	require.NoError(t, err)
	request := decoded.Inference
	require.Equal(t,
		[]inference.Modality{inference.ModalityText, inference.ModalityAudio},
		request.OutputModalities)
	require.NotNil(t, request.AudioOutput)
	require.Equal(t, "alloy", request.AudioOutput.Voice)
	require.Equal(t, "wav", request.AudioOutput.Format)
}

// TestDecodeOmittedModalitiesLeavesTheRequestUnchanged states what an ordinary
// text turn produces. The field has to stay empty rather than arrive as an
// explicit text list, because the response cache treats an empty list as the
// request that asks for nothing.
func TestDecodeOmittedModalitiesLeavesTheRequestUnchanged(t *testing.T) {
	decoded, err := DecodeChat(strings.NewReader(
		`{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"hello"}]}`))
	require.NoError(t, err)
	request := decoded.Inference
	require.Nil(t, request.OutputModalities)
	require.Nil(t, request.AudioOutput)
}

// TestEncodeGeneratedImage holds the response spelling for a picture. A
// generated image travels beside the content rather than inside it, which is
// what an OpenAI-compatible client already reads.
func TestEncodeGeneratedImage(t *testing.T) {
	encoded := EncodeChat(inference.ChatResponse{
		ID:    "chatcmpl-1",
		Model: "openai/gpt-image-1",
		Choices: []inference.Choice{{
			Index: 0,
			Message: inference.Message{
				Role: inference.RoleAssistant,
				Content: []inference.ContentPart{
					{Kind: inference.ContentText, Text: "here it is"},
					{Kind: inference.ContentImage, Image: &inference.Image{
						URL: "data:image/png;base64,AAAA",
					}},
				},
			},
			FinishReason: "stop",
		}},
	})

	require.Len(t, encoded.Choices[0].Message.Images, 1)
	image := encoded.Choices[0].Message.Images[0]
	require.Equal(t, contentTypeImageURL, image.Type)
	require.NotNil(t, image.ImageURL)
	require.Equal(t, "data:image/png;base64,AAAA", image.ImageURL.URL)

	// The words survive beside the picture. A caller that cannot render an
	// image still receives the answer.
	require.Equal(t, "here it is", encoded.Choices[0].Message.Content)

	wire, err := json.Marshal(encoded)
	require.NoError(t, err)
	require.Contains(t, string(wire), `"images":[{"type":"image_url"`)
}

// TestEncodeGeneratedAudio holds the response spelling for a spoken answer.
// The bytes are raw base64 with no data URL prefix, which is the one place
// this API differs from how it spells an image.
func TestEncodeGeneratedAudio(t *testing.T) {
	spoken := []byte{0x52, 0x49, 0x46, 0x46}
	encoded := EncodeChat(inference.ChatResponse{
		ID:    "chatcmpl-2",
		Model: "openai/gpt-4o-audio-preview",
		Choices: []inference.Choice{{
			Index: 0,
			Message: inference.Message{
				Role: inference.RoleAssistant,
				Content: []inference.ContentPart{
					{Kind: inference.ContentText, Text: "hello there"},
					{Kind: inference.ContentAudio, Audio: &inference.Audio{
						Data: spoken, Format: "wav",
					}},
				},
			},
			FinishReason: "stop",
		}},
	})

	audio := encoded.Choices[0].Message.Audio
	require.NotNil(t, audio)
	require.Equal(t, base64.StdEncoding.EncodeToString(spoken), audio.Data)
	require.Equal(t, "wav", audio.Format)
	require.Equal(t, "hello there", audio.Transcript)
}

// TestEncodeTextOnlyResponseCarriesNoMediaFields states the cost of the two
// fields above. Every text answer passes through this encoder, and a null
// images or audio key on all of them changes what an existing client parses.
func TestEncodeTextOnlyResponseCarriesNoMediaFields(t *testing.T) {
	encoded := EncodeChat(inference.ChatResponse{
		ID: "chatcmpl-3",
		Choices: []inference.Choice{{
			Message: inference.Message{
				Role:    inference.RoleAssistant,
				Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}},
			},
		}},
	})
	wire, err := json.Marshal(encoded)
	require.NoError(t, err)
	require.NotContains(t, string(wire), `"images"`)
	require.NotContains(t, string(wire), `"audio"`)
}

// TestEncodeStreamedMedia holds the streaming spelling. An image arrives whole
// in one delta and audio arrives in pieces, so the two fields behave
// differently and a single shape could not carry both.
func TestEncodeStreamedMedia(t *testing.T) {
	chunk := EncodeStream(inference.StreamEvent{
		Kind: inference.StreamDelta,
		ID:   "chatcmpl-4",
		Deltas: []inference.ChoiceDelta{{
			Index: 0,
			Media: []inference.ContentPart{{
				Kind:  inference.ContentImage,
				Image: &inference.Image{URL: "data:image/png;base64,AAAA"},
			}},
			Audio: &inference.AudioChunk{Data: []byte{0x01, 0x02}, Transcript: "hel"},
		}},
	})

	require.Len(t, chunk.Choices[0].Delta.Images, 1)
	require.Equal(t, "data:image/png;base64,AAAA", chunk.Choices[0].Delta.Images[0].ImageURL.URL)
	require.NotNil(t, chunk.Choices[0].Delta.Audio)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}), chunk.Choices[0].Delta.Audio.Data)
	require.Equal(t, "hel", chunk.Choices[0].Delta.Audio.Transcript)
}
