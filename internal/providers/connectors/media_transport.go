package connectors

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

// MediaTarget is the route binding every media provider request carries: the
// provider's own model ID, the endpoint the planner selected, and the
// credential that pays for the call. It is one embedded concept rather than the
// same three fields written on each request, so a caller that binds a route
// says it the same way whichever operation it is running.
type MediaTarget struct {
	Model string `json:"model"`
	// Endpoint and Credential are transport facts rather than request fields,
	// so neither reaches the provider body.
	Endpoint   InferenceEndpoint    `json:"-"`
	Credential credentials.Material `json:"-"`
}

// Bind applies one selected route and credential to the request that embeds it.
func (t *MediaTarget) Bind(model string, endpoint InferenceEndpoint, credential credentials.Material) {
	t.Model = model
	t.Endpoint = endpoint
	t.Credential = credential
}

// ImagesRequest is one image generation or image edit call. The endpoint the
// planner selected decides which of the two the provider performs, because the
// catalog carries a separate path for each operation.
type ImagesRequest struct {
	MediaTarget
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
	Image UploadedFile `json:"-"`
	Mask  UploadedFile `json:"-"`
}

// UploadedFile is one decoded upload held for replay across attempts.
type UploadedFile struct {
	Filename string
	// MediaType is the content type the caller stated. A provider reads it
	// to pick a decoder, so the multipart body repeats it rather than
	// letting every upload arrive as an opaque byte stream.
	MediaType string
	Bytes     []byte
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
	MediaTarget
	Input          string  `json:"input"`
	Voice          string  `json:"voice,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"`
	Speed          float64 `json:"speed,omitempty"`
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
	MediaTarget
	File           UploadedFile
	Language       string
	Prompt         string
	ResponseFormat string
	Temperature    *float64
}

// TranscriptionResponse is written speech.
type TranscriptionResponse struct {
	Text     string      `json:"text"`
	Language string      `json:"language,omitempty"`
	Duration float64     `json:"duration,omitempty"`
	Usage    *MediaUsage `json:"usage,omitempty"`
}

// RecognitionRequest is one document-recognition call. It carries the whole
// document rather than one page, because a provider that reads a document
// reads its pages in order and this gateway carries no writer that could split
// one container into many.
type RecognitionRequest struct {
	MediaTarget
	Document UploadedFile
	// Pages is the page count the native read produced. A transport reports
	// its own answer against it, so a provider that stopped early is told
	// apart from a document that ended.
	Pages int
}

// RecognizedPage is the text one page carried.
type RecognizedPage struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

// RecognitionResponse is the text a provider read off a document's pages.
type RecognitionResponse struct {
	Pages []RecognizedPage `json:"pages"`
	Usage *MediaUsage      `json:"usage,omitempty"`
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

// DocumentRecognizer is the narrow optional interface for
// documents-recognition. A transport implements it when the provider reads
// text off a page that carries none.
type DocumentRecognizer interface {
	RecognizeDocument(ctx context.Context, request *RecognitionRequest) (*RecognitionResponse, error)
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

// DocumentRecognizerFor returns the recognition transport a route selected.
func DocumentRecognizerFor(
	connector Connector,
	endpointType catalogs.EndpointType,
) (DocumentRecognizer, bool) {
	transport, found := selectTransport(connector, endpointType)
	if !found {
		return nil, false
	}
	recognizer, implemented := transport.(DocumentRecognizer)
	return recognizer, implemented
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
