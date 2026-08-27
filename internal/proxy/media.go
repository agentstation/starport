package proxy

import (
	"context"

	"github.com/agentstation/starport/internal/router"
)

// ProcessImages handles image generation and image edit requests.
func (p *proxy) ProcessImages(ctx context.Context, req *ImagesRequest) (*ImagesResponse, error) {
	if err := ValidateImagesRequest(req); err != nil {
		return nil, err
	}
	return processMedia(ctx, req, req.Request.Model, p.router.RouteImages)
}

// ProcessSpeech handles text-to-speech requests.
func (p *proxy) ProcessSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error) {
	if err := ValidateSpeechRequest(req); err != nil {
		return nil, err
	}
	return processMedia(ctx, req, req.Request.Model, p.router.RouteSpeech)
}

// ProcessTranscription handles speech-to-text requests.
func (p *proxy) ProcessTranscription(
	ctx context.Context,
	req *TranscriptionRequest,
) (*TranscriptionResponse, error) {
	if err := ValidateTranscriptionRequest(req); err != nil {
		return nil, err
	}
	return processMedia(ctx, req, req.Request.Model, p.router.RouteTranscription)
}

// processMedia hands one validated media request to the router and carries the
// route evidence back. The three operations differ only in the router method
// they call, so the identity transfer and the failure wrapping are written
// once.
func processMedia[Request, Response any](
	ctx context.Context,
	req *MediaRequest[Request],
	model string,
	route func(context.Context, *router.MediaRequest[Request]) (*router.MediaResponse[Response], error),
) (*MediaResponse[Response], error) {
	result, err := route(ctx, &router.MediaRequest[Request]{
		Request:      req.Request,
		APIKeyConfig: transformAPIKeyConfig(req.APIKeyConfig),
		TenantID:     req.TenantID,
	})
	if err != nil {
		return nil, &RoutingError{
			Model: model, Reason: "failed to route media request", Err: err,
		}
	}
	response := &MediaResponse[Response]{
		Response:         result.Response,
		ModelUsed:        result.ModelUsed,
		ProviderUsed:     result.ProviderUsed,
		CredentialSource: result.CredentialSource,
		Attempts:         result.Attempts,
		CatalogSnapshot:  result.CatalogSnapshot,
	}
	if result.Metadata != nil {
		response.RoutingDuration = result.Metadata.RoutingDuration
	}
	return response, nil
}
