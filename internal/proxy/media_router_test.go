package proxy

import (
	"context"
	"errors"

	"github.com/agentstation/starport/internal/jobs"
	routepkg "github.com/agentstation/starport/internal/router"
)

// unroutedOperations answers every route past chat and embeddings for a test
// router that exercises a different path. A fake gains them by embedding it,
// so adding an operation to ModelRouter does not edit every fake in this
// package.
type unroutedOperations struct{}

func (unroutedOperations) RouteRerank(
	context.Context,
	*routepkg.RerankRequest,
) (*routepkg.RerankResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedOperations) RouteModerations(
	context.Context,
	*routepkg.ModerationRequest,
) (*routepkg.ModerationResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedOperations) RouteImages(
	context.Context,
	*routepkg.ImagesRequest,
) (*routepkg.ImagesResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedOperations) RouteSpeech(
	context.Context,
	*routepkg.SpeechRequest,
) (*routepkg.SpeechResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedOperations) RouteTranscription(
	context.Context,
	*routepkg.TranscriptionRequest,
) (*routepkg.TranscriptionResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedOperations) RouteVideoSubmit(
	context.Context,
	*routepkg.VideoSubmitRequest,
) (*routepkg.VideoJobResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedOperations) RouteVideoPoll(
	context.Context,
	*routepkg.VideoJobRequest,
) (*routepkg.VideoJobResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedOperations) RouteVideoCancel(
	context.Context,
	*routepkg.VideoJobRequest,
) (*routepkg.VideoJobResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedOperations) RouteVideoContent(
	context.Context,
	*routepkg.VideoAssetRequest,
) (*routepkg.VideoAssetResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedOperations) RouteDocumentRecognition(
	context.Context,
	*routepkg.RecognitionRequest,
) (*routepkg.RecognitionResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

// unsupportedOperationProxy answers every operation past chat and embeddings
// for a Proxy mock that exercises a different path.
type unsupportedOperationProxy struct{}

func (unsupportedOperationProxy) ProcessRerank(
	context.Context,
	*RerankRequest,
) (*RerankResponse, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperationProxy) ProcessModerations(
	context.Context,
	*ModerationRequest,
) (*ModerationResponse, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperationProxy) ProcessImages(
	context.Context,
	*ImagesRequest,
) (*ImagesResponse, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperationProxy) ProcessSpeech(
	context.Context,
	*SpeechRequest,
) (*SpeechResponse, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperationProxy) ProcessTranscription(
	context.Context,
	*TranscriptionRequest,
) (*TranscriptionResponse, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperationProxy) SubmitVideoJob(
	context.Context,
	*VideoSubmitRequest,
) (*VideoJobAnswer, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperationProxy) PollVideoJob(
	context.Context,
	*VideoJobRequest,
) (*VideoJobAnswer, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperationProxy) CancelVideoJob(
	context.Context,
	*VideoJobRequest,
) (*VideoJobAnswer, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperationProxy) FetchVideoAsset(
	context.Context,
	*VideoAssetRequest,
) (*VideoAsset, error) {
	return nil, errUnsupportedOperation
}

// VideoJobRunner answers no provider side, so a record store handed this mock
// refuses to start work rather than starting work nothing serves.
func (unsupportedOperationProxy) VideoJobRunner(*VideoSubmitRequest) jobs.Runner {
	return nil
}

// errUnsupportedOperation is the answer a mock without this path gives.
var errUnsupportedOperation = errors.New("operation is not part of this test")
