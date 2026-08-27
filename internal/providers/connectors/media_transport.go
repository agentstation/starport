package connectors

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

// ImagesRequest is one image generation or image edit call. The endpoint the
// planner selected decides which of the two the provider performs, because the
// catalog carries a separate path for each operation.
type ImagesRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Style          string `json:"style,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	User           string `json:"user,omitempty"`

	// Image and Mask carry the decoded upload of an edit request. Bytes stay
	// on the request so a retry replays the same upload without asking the
	// caller to send it again.
	Image      UploadedFile `json:"-"`
	Mask       UploadedFile `json:"-"`
	Endpoint   InferenceEndpoint
	Credential credentials.Material `json:"-"`
}

// UploadedFile is one decoded upload held for replay across attempts.
type UploadedFile struct {
	Filename string
	Bytes    []byte
}

// Present reports whether the upload carries content.
func (f UploadedFile) Present() bool { return len(f.Bytes) > 0 }

// ImagesResponse is the provider answer to an image call.
type ImagesResponse struct {
	Created int64        `json:"created"`
	Data    []ImageDatum `json:"data"`
	Usage   *MediaUsage  `json:"usage,omitempty"`
}

// ImageDatum is one generated image, either inline or by reference.
type ImageDatum struct {
	B64JSON       string `json:"b64_json,omitempty"`
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// MediaUsage is the unit count a provider reports for a media call. A provider
// that reports nothing leaves it absent, and the cost seam names the gap rather
// than inventing a number.
type MediaUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

// SpeechRequest is one text-to-speech call.
type SpeechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"`
	Speed          float64 `json:"speed,omitempty"`

	Endpoint   InferenceEndpoint    `json:"-"`
	Credential credentials.Material `json:"-"`
}

// SpeechResponse is generated audio. A speech endpoint answers with the encoded
// file itself rather than with JSON, so the bytes and their media type are the
// whole answer.
type SpeechResponse struct {
	Audio       []byte
	ContentType string
}

// TranscriptionRequest is one speech-to-text call. One request shape serves
// both transcription and translation, because the two differ only in the
// endpoint the catalog names.
type TranscriptionRequest struct {
	Model          string
	File           UploadedFile
	Language       string
	Prompt         string
	ResponseFormat string
	Temperature    *float64

	Endpoint   InferenceEndpoint
	Credential credentials.Material
}

// TranscriptionResponse is written speech.
type TranscriptionResponse struct {
	Text     string      `json:"text"`
	Language string      `json:"language,omitempty"`
	Duration float64     `json:"duration,omitempty"`
	Usage    *MediaUsage `json:"usage,omitempty"`
}

// ImageGenerator is the narrow optional interface a transport implements to
// serve images-generations and images-edits. Connector does not carry it. A
// chat-only transport would have to answer a method it cannot perform, and the
// compiler would stop reporting the difference.
type ImageGenerator interface {
	GenerateImages(ctx context.Context, request *ImagesRequest) (*ImagesResponse, error)
}

// SpeechSynthesizer is the narrow optional interface for audio-speech.
type SpeechSynthesizer interface {
	SynthesizeSpeech(ctx context.Context, request *SpeechRequest) (*SpeechResponse, error)
}

// Transcriber is the narrow optional interface for audio-transcriptions and
// audio-translations.
type Transcriber interface {
	Transcribe(ctx context.Context, request *TranscriptionRequest) (*TranscriptionResponse, error)
}

// TransportLookup exposes one compiled transport by endpoint type. A caller
// probes the transport the planner selected rather than the composed
// connector, because one provider connector can span protocols that do not
// implement the same media interfaces.
type TransportLookup interface {
	Transport(endpointType catalogs.EndpointType) (Connector, bool)
}

// ImageGeneratorFor returns the image transport a route selected.
func ImageGeneratorFor(
	connector Connector,
	endpointType catalogs.EndpointType,
) (ImageGenerator, bool) {
	transport, found := selectTransport(connector, endpointType)
	if !found {
		return nil, false
	}
	generator, implemented := transport.(ImageGenerator)
	return generator, implemented
}

// SpeechSynthesizerFor returns the speech transport a route selected.
func SpeechSynthesizerFor(
	connector Connector,
	endpointType catalogs.EndpointType,
) (SpeechSynthesizer, bool) {
	transport, found := selectTransport(connector, endpointType)
	if !found {
		return nil, false
	}
	synthesizer, implemented := transport.(SpeechSynthesizer)
	return synthesizer, implemented
}

// TranscriberFor returns the transcription transport a route selected.
func TranscriberFor(
	connector Connector,
	endpointType catalogs.EndpointType,
) (Transcriber, bool) {
	transport, found := selectTransport(connector, endpointType)
	if !found {
		return nil, false
	}
	transcriber, implemented := transport.(Transcriber)
	return transcriber, implemented
}

func selectTransport(
	connector Connector,
	endpointType catalogs.EndpointType,
) (Connector, bool) {
	if connector == nil {
		return nil, false
	}
	lookup, composed := connector.(TransportLookup)
	if !composed {
		return connector, true
	}
	return lookup.Transport(endpointType)
}
