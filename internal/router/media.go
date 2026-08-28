package router

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

// The three media routes reuse the chat route plan, credential policy,
// availability state, and total-attempt budget. What differs is the operation
// they plan for and the narrow transport interface they call, so only the
// provider call itself is written once per operation.

// MediaRequest routes one canonical media request. The shared carrier is
// OperationRequest.
type MediaRequest[Request any] = OperationRequest[Request]

// MediaResponse is one media result. The shared carrier is
// OperationResponse: reranking and document recognition are not media, and a
// media name on the answer they return would say they were.
type MediaResponse[Response any] = OperationResponse[Response]

// ImagesRequest routes one image generation or image edit.
type ImagesRequest = MediaRequest[inference.ImagesRequest]

// ImagesResponse is one image result with route evidence.
type ImagesResponse = MediaResponse[inference.ImagesResponse]

// SpeechRequest routes one text-to-speech call.
type SpeechRequest = MediaRequest[inference.SpeechRequest]

// SpeechResponse is one speech result with route evidence.
type SpeechResponse = MediaResponse[inference.SpeechResponse]

// TranscriptionRequest routes one speech-to-text call.
type TranscriptionRequest = MediaRequest[inference.TranscriptionRequest]

// TranscriptionResponse is one transcript with route evidence.
type TranscriptionResponse = MediaResponse[inference.TranscriptionResponse]

// RouteImages executes one image generation or image edit request. The
// presence of a source image selects the operation, because the catalog names
// a separate one for an edit and a provider serves it at its own path.
func (r *modelRouter) RouteImages(ctx context.Context, req *ImagesRequest) (*ImagesResponse, error) {
	if req == nil || req.Request.Model == "" {
		return nil, ErrNoModelsAvailable
	}
	operation := routing.OperationImagesGenerations
	if req.Request.IsEdit() {
		operation = routing.OperationImagesEdits
	}
	call := providerCall[*connectors.ImagesRequest, *connectors.ImagesResponse, inference.ImagesResponse]{
		transport: imageTransport,
		build:     func() *connectors.ImagesRequest { return connectors.ImagesRequestFromInference(req.Request) },
		convert:   connectors.ImagesResponseToInference,
	}
	return routeOperation(ctx, r, req.policy(req.Request.Model), operation,
		inference.ImagesResponse.Clone, call.attempt(operation))
}

// RouteSpeech executes one text-to-speech request.
func (r *modelRouter) RouteSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error) {
	if req == nil || req.Request.Model == "" {
		return nil, ErrNoModelsAvailable
	}
	call := providerCall[*connectors.SpeechRequest, *connectors.SpeechResponse, inference.SpeechResponse]{
		transport: speechTransport,
		build:     func() *connectors.SpeechRequest { return connectors.SpeechRequestFromInference(req.Request) },
		convert:   connectors.SpeechResponseToInference,
	}
	return routeOperation(ctx, r, req.policy(req.Request.Model), routing.OperationAudioSpeech,
		inference.SpeechResponse.Clone, call.attempt(routing.OperationAudioSpeech))
}

// RouteTranscription executes one speech-to-text request. The request states
// whether it wants the spoken language or English, and that answer selects the
// operation, because a provider exposes the two at separate paths.
func (r *modelRouter) RouteTranscription(
	ctx context.Context,
	req *TranscriptionRequest,
) (*TranscriptionResponse, error) {
	if req == nil || req.Request.Model == "" {
		return nil, ErrNoModelsAvailable
	}
	operation := routing.OperationAudioTranscriptions
	if req.Request.Translate {
		operation = routing.OperationAudioTranslations
	}
	call := providerCall[*connectors.TranscriptionRequest, *connectors.TranscriptionResponse, inference.TranscriptionResponse]{
		transport: transcriptionTransport,
		build: func() *connectors.TranscriptionRequest {
			return connectors.TranscriptionRequestFromInference(req.Request)
		},
		convert: connectors.TranscriptionResponseToInference,
	}
	return routeOperation(ctx, r, req.policy(req.Request.Model), operation,
		inference.TranscriptionResponse.Clone, call.attempt(operation))
}

func imageTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (providerInvoke[*connectors.ImagesRequest, *connectors.ImagesResponse], bool) {
	generator, implemented := connectors.ImageGeneratorFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return generator.GenerateImages, true
}

func speechTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (providerInvoke[*connectors.SpeechRequest, *connectors.SpeechResponse], bool) {
	synthesizer, implemented := connectors.SpeechSynthesizerFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return synthesizer.SynthesizeSpeech, true
}

func transcriptionTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (providerInvoke[*connectors.TranscriptionRequest, *connectors.TranscriptionResponse], bool) {
	transcriber, implemented := connectors.TranscriberFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return transcriber.Transcribe, true
}
