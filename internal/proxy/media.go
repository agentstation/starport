package proxy

import (
	"context"
)

// ProcessImages handles image generation and image edit requests.
func (p *proxy) ProcessImages(ctx context.Context, req *ImagesRequest) (*ImagesResponse, error) {
	if err := ValidateImagesRequest(req); err != nil {
		return nil, err
	}
	return processOperation(ctx, req, req.Request.Model, p.router.RouteImages)
}

// ProcessSpeech handles text-to-speech requests.
func (p *proxy) ProcessSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error) {
	if err := ValidateSpeechRequest(req); err != nil {
		return nil, err
	}
	return processOperation(ctx, req, req.Request.Model, p.router.RouteSpeech)
}

// ProcessTranscription handles speech-to-text requests.
func (p *proxy) ProcessTranscription(
	ctx context.Context,
	req *TranscriptionRequest,
) (*TranscriptionResponse, error) {
	if err := ValidateTranscriptionRequest(req); err != nil {
		return nil, err
	}
	return processOperation(ctx, req, req.Request.Model, p.router.RouteTranscription)
}
