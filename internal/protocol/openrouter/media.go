package openrouter

import (
	"io"
	"mime/multipart"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/protocol/mediaform"
)

// OpenRouter publishes three media paths: one image path, one speech path, and
// one transcription path. It publishes no image edit path and no translation
// path, so those two operations reach the gateway on the OpenAI family alone.
// The request bodies match the OpenAI ones; the answers add the provider that
// served the call, which is what every OpenRouter answer carries.

// ImagesRequest is the OpenRouter image wire request.
type ImagesRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Style          string `json:"style,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	User           string `json:"user,omitempty"`
}

// DecodeImages decodes one strict OpenRouter image request.
func DecodeImages(reader io.Reader) (inference.ImagesRequest, error) {
	var wire ImagesRequest
	if err := decodeStrict(reader, &wire); err != nil {
		return inference.ImagesRequest{}, err
	}
	return inference.ImagesRequest{
		Model: wire.Model, Prompt: wire.Prompt, N: wire.N, Size: wire.Size,
		Quality: wire.Quality, Style: wire.Style,
		ResponseFormat: wire.ResponseFormat, User: wire.User,
	}, nil
}

// ImagesResponse is the OpenRouter image wire response.
type ImagesResponse struct {
	Created  int64        `json:"created"`
	Model    string       `json:"model"`
	Provider string       `json:"provider,omitempty"`
	Data     []ImageDatum `json:"data"`
	Usage    *Usage       `json:"usage,omitempty"`
}

// ImageDatum is one generated image, inline or by reference.
type ImageDatum struct {
	B64JSON       string `json:"b64_json,omitempty"`
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// EncodeImages converts one canonical image result to OpenRouter wire values.
func EncodeImages(response inference.ImagesResponse) ImagesResponse {
	data := make([]ImageDatum, len(response.Images))
	for index, image := range response.Images {
		data[index] = ImageDatum{
			B64JSON: image.B64JSON, URL: image.URL, RevisedPrompt: image.RevisedPrompt,
		}
	}
	wire := ImagesResponse{
		Created: response.CreatedUnix, Model: response.Model,
		Provider: providerFromModel(response.Model), Data: data,
	}
	if usage := encodeUsage(response.Usage); usage != (Usage{}) {
		wire.Usage = &usage
	}
	return wire
}

// SpeechRequest is the OpenRouter text-to-speech wire request.
type SpeechRequest struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          string   `json:"voice,omitempty"`
	ResponseFormat string   `json:"response_format,omitempty"`
	Speed          *float64 `json:"speed,omitempty"`
}

// DecodeSpeech decodes one strict OpenRouter text-to-speech request.
func DecodeSpeech(reader io.Reader) (inference.SpeechRequest, error) {
	var wire SpeechRequest
	if err := decodeStrict(reader, &wire); err != nil {
		return inference.SpeechRequest{}, err
	}
	return inference.SpeechRequest{
		Model: wire.Model, Input: wire.Input, Voice: wire.Voice,
		ResponseFormat: wire.ResponseFormat, Speed: wire.Speed,
	}, nil
}

// DecodeTranscriptionForm decodes one OpenRouter speech-to-text request from
// multipart form data.
func DecodeTranscriptionForm(form *multipart.Form) (inference.TranscriptionRequest, error) {
	return mediaform.Transcription(form, false)
}

// TranscriptionResponse is the OpenRouter speech-to-text wire response.
type TranscriptionResponse struct {
	Text     string  `json:"text"`
	Model    string  `json:"model,omitempty"`
	Provider string  `json:"provider,omitempty"`
	Language string  `json:"language,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

// EncodeTranscription converts one canonical transcript to OpenRouter wire
// values.
func EncodeTranscription(response inference.TranscriptionResponse) TranscriptionResponse {
	return TranscriptionResponse{
		Text: response.Text, Model: response.Model,
		Provider: providerFromModel(response.Model),
		Language: response.Language, Duration: response.Duration,
	}
}
