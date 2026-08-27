package openai

import (
	"io"
	"mime/multipart"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/protocol/mediaform"
)

// The dedicated media operations carry two request encodings. A generation and
// a speech call send JSON. An edit and a transcription send multipart form
// data, because they carry a file. The codec owns both, so the wire field
// names live in one package and the controller stays HTTP mechanics.

// ImagesRequest is the OpenAI image generation wire request.
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

// DecodeImages decodes one strict OpenAI image generation request.
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

// DecodeImagesForm decodes one OpenAI image edit request from multipart form
// data.
func DecodeImagesForm(form *multipart.Form) (inference.ImagesRequest, error) {
	return mediaform.Images(form)
}

// ImagesResponse is the OpenAI image wire response.
type ImagesResponse struct {
	Created int64        `json:"created"`
	Data    []ImageDatum `json:"data"`
	Usage   *Usage       `json:"usage,omitempty"`
}

// ImageDatum is one generated image, inline or by reference.
type ImageDatum struct {
	B64JSON       string `json:"b64_json,omitempty"`
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// EncodeImages converts one canonical image result to OpenAI wire values.
func EncodeImages(response inference.ImagesResponse) ImagesResponse {
	data := make([]ImageDatum, len(response.Images))
	for index, image := range response.Images {
		data[index] = ImageDatum{
			B64JSON: image.B64JSON, URL: image.URL, RevisedPrompt: image.RevisedPrompt,
		}
	}
	wire := ImagesResponse{Created: response.CreatedUnix, Data: data}
	if usage := encodeUsage(response.Usage); usage != (Usage{}) {
		wire.Usage = &usage
	}
	return wire
}

// SpeechRequest is the OpenAI text-to-speech wire request.
type SpeechRequest struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          string   `json:"voice,omitempty"`
	ResponseFormat string   `json:"response_format,omitempty"`
	Speed          *float64 `json:"speed,omitempty"`
}

// DecodeSpeech decodes one strict OpenAI text-to-speech request.
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

// DecodeTranscriptionForm decodes one OpenAI speech-to-text request from
// multipart form data. translate states whether the caller reached the
// translation path.
func DecodeTranscriptionForm(form *multipart.Form, translate bool) (inference.TranscriptionRequest, error) {
	return mediaform.Transcription(form, translate)
}

// TranscriptionResponse is the OpenAI speech-to-text wire response.
type TranscriptionResponse struct {
	Text     string  `json:"text"`
	Language string  `json:"language,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

// EncodeTranscription converts one canonical transcript to OpenAI wire values.
func EncodeTranscription(response inference.TranscriptionResponse) TranscriptionResponse {
	return TranscriptionResponse{
		Text: response.Text, Language: response.Language, Duration: response.Duration,
	}
}
