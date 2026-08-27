package connectors

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

// mediaSamples builds one canonical part per content kind, each carrying a
// payload. A transport that keeps the type and drops the payload passes a
// test written against empty parts, so every sample here holds real bytes or
// a real reference.
func mediaSamples() map[inference.ContentKind]inference.ContentPart {
	return map[inference.ContentKind]inference.ContentPart{
		inference.ContentText: {Kind: inference.ContentText, Text: "describe this"},
		inference.ContentImage: {
			Kind:  inference.ContentImage,
			Image: &inference.Image{URL: "data:image/png;base64,AAAA", Detail: "high"},
		},
		inference.ContentAudio: {
			Kind:  inference.ContentAudio,
			Audio: &inference.Audio{Data: []byte{0x52, 0x49, 0x46, 0x46}, Format: "mp3"},
		},
		inference.ContentDocument: {
			Kind: inference.ContentDocument,
			Document: &inference.Document{
				URL:      "data:application/pdf;base64,JVBERg==",
				Filename: "report.pdf",
			},
		},
		inference.ContentVideo: {
			Kind:  inference.ContentVideo,
			Video: &inference.Video{URL: "https://example.test/clip.mp4"},
		},
	}
}

// TestOpenAIShapedTransportKeepsEveryPayload walks every content kind through
// the adapter both ways. The defect it holds shut is the one the adapter had:
// a part reached the provider as a bare type string with its payload gone,
// and the provider answered about media it never received.
func TestOpenAIShapedTransportKeepsEveryPayload(t *testing.T) {
	samples := mediaSamples()
	for _, kind := range inference.ContentKinds() {
		sample, ok := samples[kind]
		require.Truef(t, ok, "content kind %q has no adapter coverage", kind)

		t.Run(string(kind), func(t *testing.T) {
			request, err := ChatRequestFromInference(inference.ChatRequest{
				Model:    "openai/gpt-4.1",
				Messages: []inference.Message{{Role: inference.RoleUser, Content: []inference.ContentPart{sample}}},
			})
			require.NoError(t, err)

			parts, err := ParseMessageContent(request.Messages[0].Content)
			require.NoError(t, err)
			require.Len(t, parts, 1)

			if kind != inference.ContentText {
				payload, hasMedia := partMediaPayload(parts[0])
				require.Truef(t, hasMedia, "wire part %+v carries no payload", parts[0])
				require.NotEmptyf(t, payload.Base64+payload.URL, "wire part %+v carries an empty payload", parts[0])
			}

			back, err := contentToInference(parts[0])
			require.NoError(t, err)
			require.Equal(t, sample, back)
		})
	}
}

// TestAnthropicKeepsOrRefusesEveryKind holds the rule that gives this seam its
// value. Anthropic serves images and documents and serves neither audio nor
// video, so every kind either arrives with its payload or fails by name.
func TestAnthropicKeepsOrRefusesEveryKind(t *testing.T) {
	cases := []struct {
		name    string
		part    ContentPart
		block   string
		source  string
		refused bool
	}{
		{
			name:   "inline image",
			part:   ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"}},
			block:  "image",
			source: "base64",
		},
		{
			name:   "referenced image",
			part:   ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: "https://example.test/cat.png"}},
			block:  "image",
			source: "url",
		},
		{
			name:   "inline document",
			part:   ContentPart{Type: "file", File: &File{Filename: "a.pdf", FileData: "data:application/pdf;base64,JVBERg=="}},
			block:  "document",
			source: "base64",
		},
		{
			name:    "audio",
			part:    ContentPart{Type: "input_audio", InputAudio: &InputAudio{Data: "UklGRg==", Format: "wav"}},
			refused: true,
		},
		{
			name:    "video",
			part:    ContentPart{Type: "video_url", VideoURL: &VideoURL{URL: "https://example.test/clip.mp4"}},
			refused: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			blocks, err := anthropicContentBlocks([]ContentPart{testCase.part})
			if testCase.refused {
				require.ErrorIs(t, err, ErrContentKindUnsupported)
				return
			}
			require.NoError(t, err)
			require.Len(t, blocks, 1)
			require.Equal(t, testCase.block, blocks[0][wireTypeToken])
			source := blocks[0]["source"].(map[string]any)
			require.Equal(t, testCase.source, source[wireTypeToken])
			if testCase.source == "base64" {
				require.NotEmpty(t, source["data"])
				require.NotEmpty(t, source["media_type"])
			} else {
				require.NotEmpty(t, source["url"])
			}
		})
	}
}

// TestGeminiKeepsEveryMediaPayload proves the Google shape carries each kind.
// Gemini refuses none of them, so the failure this catches is a part that
// arrives as an empty text entry rather than as inline data.
func TestGeminiKeepsEveryMediaPayload(t *testing.T) {
	cases := []struct {
		name      string
		part      ContentPart
		field     string
		mediaType string
	}{
		{
			name:      "audio names its media type, not its container",
			part:      ContentPart{Type: "input_audio", InputAudio: &InputAudio{Data: "UklGRg==", Format: "mp3"}},
			field:     "inline_data",
			mediaType: "audio/mpeg",
		},
		{
			name:      "inline document",
			part:      ContentPart{Type: "file", File: &File{FileData: "data:application/pdf;base64,JVBERg=="}},
			field:     "inline_data",
			mediaType: "application/pdf",
		},
		{
			name:  "referenced video",
			part:  ContentPart{Type: "video_url", VideoURL: &VideoURL{URL: "https://example.test/clip.mp4"}},
			field: "file_data",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			parts := geminiParts([]ContentPart{testCase.part})
			require.Len(t, parts, 1)
			entry, ok := parts[0][testCase.field].(map[string]any)
			require.Truef(t, ok, "part %+v has no %s", parts[0], testCase.field)
			if testCase.field == "inline_data" {
				require.Equal(t, testCase.mediaType, entry["mime_type"])
				require.NotEmpty(t, entry["data"])
			} else {
				require.Equal(t, "https://example.test/clip.mp4", entry["file_uri"])
			}
		})
	}
}

// TestContentPartRefusesAnUnknownKind proves the refusal is a named error on
// both sides. A caller of the adapter tests for it rather than matching a
// message string.
func TestContentPartRefusesAnUnknownKind(t *testing.T) {
	_, err := contentFromInference(inference.ContentPart{Kind: inference.ContentKind("hologram")})
	require.True(t, errors.Is(err, ErrContentKindUnsupported))
	require.Contains(t, err.Error(), "hologram")

	_, err = contentToInference(ContentPart{Type: "hologram_url"})
	require.True(t, errors.Is(err, ErrContentKindUnsupported))
	require.Contains(t, err.Error(), "hologram_url")
}

// TestContentPartRefusesAStrippedPayload proves that a part naming a media
// type with nothing behind it fails. Before this seam existed such a part
// decoded to empty text, which reads to the model as a caller who sent
// nothing at all.
func TestContentPartRefusesAStrippedPayload(t *testing.T) {
	for _, wireType := range []string{"image_url", "input_audio", "file", "video_url"} {
		t.Run(wireType, func(t *testing.T) {
			_, err := contentToInference(ContentPart{Type: wireType})
			require.ErrorIs(t, err, ErrInvalidMessageContent)
		})
	}
}

// TestAudioRoundTripsThroughBase64 proves the adapter owns the base64 step in
// both directions. The canonical part holds bytes and the wire holds text, so
// a missing decode would reach the provider as a base64 string of base64.
func TestAudioRoundTripsThroughBase64(t *testing.T) {
	original := []byte{0x52, 0x49, 0x46, 0x46, 0x00}
	wire, err := contentFromInference(inference.ContentPart{
		Kind:  inference.ContentAudio,
		Audio: &inference.Audio{Data: original, Format: "wav"},
	})
	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString(original), wire.InputAudio.Data)

	back, err := contentToInference(wire)
	require.NoError(t, err)
	require.Equal(t, original, back.Audio.Data)
}
