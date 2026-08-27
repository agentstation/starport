package connectors

import (
	"fmt"

	"github.com/agentstation/starport/internal/inference"
)

// The media adapters convert between the canonical media types and the
// provider wire shapes, the way the chat and embedding adapters do. The
// canonical type is what the gateway plans, retries, meters, and answers with;
// the wire type is what one protocol family sends.

// ImagesRequestFromInference converts a canonical image request.
func ImagesRequestFromInference(request inference.ImagesRequest) *ImagesRequest {
	return &ImagesRequest{
		MediaTarget:    MediaTarget{Model: request.Model},
		Prompt:         request.Prompt,
		N:              request.N,
		Size:           request.Size,
		Quality:        request.Quality,
		Style:          request.Style,
		ResponseFormat: request.ResponseFormat,
		User:           request.User,
		Image:          uploadFromInference(request.Image),
		Mask:           uploadFromInference(request.Mask),
	}
}

// ImagesResponseToInference converts a provider image response.
func ImagesResponseToInference(response *ImagesResponse) (inference.ImagesResponse, error) {
	if response == nil {
		return inference.ImagesResponse{}, fmt.Errorf("image response is required")
	}
	images := make([]inference.GeneratedImage, len(response.Data))
	for index, datum := range response.Data {
		images[index] = inference.GeneratedImage{
			B64JSON:       datum.B64JSON,
			URL:           datum.URL,
			RevisedPrompt: datum.RevisedPrompt,
		}
	}
	return inference.ImagesResponse{
		CreatedUnix: response.Created,
		Images:      images,
		Usage:       mediaUsageToInference(response.Usage, len(images)),
	}, nil
}

// SpeechRequestFromInference converts a canonical speech request.
func SpeechRequestFromInference(request inference.SpeechRequest) *SpeechRequest {
	converted := &SpeechRequest{
		MediaTarget:    MediaTarget{Model: request.Model},
		Input:          request.Input,
		Voice:          request.Voice,
		ResponseFormat: request.ResponseFormat,
	}
	if request.Speed != nil {
		converted.Speed = *request.Speed
	}
	return converted
}

// SpeechResponseToInference converts a provider speech response.
func SpeechResponseToInference(response *SpeechResponse) (inference.SpeechResponse, error) {
	if response == nil {
		return inference.SpeechResponse{}, fmt.Errorf("speech response is required")
	}
	if len(response.Audio) == 0 {
		// A speech call that answered 200 with no bytes produced no audio.
		// Reporting it as a result would hand the caller an empty file and
		// charge for it.
		return inference.SpeechResponse{}, fmt.Errorf("speech response carries no audio")
	}
	return inference.SpeechResponse{
		Audio:       append([]byte(nil), response.Audio...),
		ContentType: response.ContentType,
	}, nil
}

// TranscriptionRequestFromInference converts a canonical transcription request.
func TranscriptionRequestFromInference(request inference.TranscriptionRequest) *TranscriptionRequest {
	return &TranscriptionRequest{
		MediaTarget:    MediaTarget{Model: request.Model},
		File:           uploadFromInference(request.File),
		Language:       request.Language,
		Prompt:         request.Prompt,
		ResponseFormat: request.ResponseFormat,
		Temperature:    request.Temperature,
	}
}

// TranscriptionResponseToInference converts a provider transcription response.
func TranscriptionResponseToInference(response *TranscriptionResponse) (inference.TranscriptionResponse, error) {
	if response == nil {
		return inference.TranscriptionResponse{}, fmt.Errorf("transcription response is required")
	}
	return inference.TranscriptionResponse{
		Text:     response.Text,
		Language: response.Language,
		Duration: response.Duration,
		Usage:    mediaUsageToInference(response.Usage, 0),
	}, nil
}

func uploadFromInference(upload inference.UploadedFile) UploadedFile {
	return UploadedFile{
		Filename:  upload.Filename,
		MediaType: upload.MediaType,
		Bytes:     upload.Bytes,
	}
}

// mediaUsageToInference carries the counts a provider reported. A provider that
// reported none leaves the token totals at zero, which the cost seam already
// reads as "no usage" rather than as a free call. The generated image count is
// separate: the gateway counted the pictures itself, so it is a measurement
// even when the provider reported no tokens.
func mediaUsageToInference(usage *MediaUsage, generatedImages int) inference.Usage {
	converted := inference.Usage{GeneratedImages: generatedImages}
	if usage == nil {
		return converted
	}
	converted.InputTokens = usage.InputTokens
	converted.OutputTokens = usage.OutputTokens
	converted.TotalTokens = usage.TotalTokens
	if converted.TotalTokens == 0 {
		converted.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return converted
}
