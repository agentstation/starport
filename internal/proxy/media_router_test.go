package proxy

import (
	"context"
	"errors"

	"github.com/agentstation/starport/internal/jobs"
	routepkg "github.com/agentstation/starport/internal/router"
)

// unroutedMedia answers the three media routes for a test router that
// exercises a different path. A fake gains them by embedding it, so adding a
// media operation to ModelRouter does not edit every fake in this package.
type unroutedMedia struct{}

func (unroutedMedia) RouteImages(
	context.Context,
	*routepkg.ImagesRequest,
) (*routepkg.ImagesResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedMedia) RouteSpeech(
	context.Context,
	*routepkg.SpeechRequest,
) (*routepkg.SpeechResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedMedia) RouteTranscription(
	context.Context,
	*routepkg.TranscriptionRequest,
) (*routepkg.TranscriptionResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedMedia) RouteVideoSubmit(
	context.Context,
	*routepkg.VideoSubmitRequest,
) (*routepkg.VideoJobResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedMedia) RouteVideoPoll(
	context.Context,
	*routepkg.VideoJobRequest,
) (*routepkg.VideoJobResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

func (unroutedMedia) RouteVideoCancel(
	context.Context,
	*routepkg.VideoJobRequest,
) (*routepkg.VideoJobResponse, error) {
	return nil, routepkg.ErrNoModelsAvailable
}

// unsupportedMediaProxy answers the three media operations for a Proxy mock
// that exercises a different path.
type unsupportedMediaProxy struct{}

func (unsupportedMediaProxy) ProcessImages(
	context.Context,
	*ImagesRequest,
) (*ImagesResponse, error) {
	return nil, errUnsupportedMedia
}

func (unsupportedMediaProxy) ProcessSpeech(
	context.Context,
	*SpeechRequest,
) (*SpeechResponse, error) {
	return nil, errUnsupportedMedia
}

func (unsupportedMediaProxy) ProcessTranscription(
	context.Context,
	*TranscriptionRequest,
) (*TranscriptionResponse, error) {
	return nil, errUnsupportedMedia
}

func (unsupportedMediaProxy) SubmitVideoJob(
	context.Context,
	*VideoSubmitRequest,
) (*VideoJobAnswer, error) {
	return nil, errUnsupportedMedia
}

func (unsupportedMediaProxy) PollVideoJob(
	context.Context,
	*VideoJobRequest,
) (*VideoJobAnswer, error) {
	return nil, errUnsupportedMedia
}

func (unsupportedMediaProxy) CancelVideoJob(
	context.Context,
	*VideoJobRequest,
) (*VideoJobAnswer, error) {
	return nil, errUnsupportedMedia
}

// VideoJobRunner answers no provider side, so a record store handed this mock
// refuses to start work rather than starting work nothing serves.
func (unsupportedMediaProxy) VideoJobRunner(*VideoSubmitRequest) jobs.Runner {
	return nil
}

// errUnsupportedMedia is the answer a mock without a media path gives.
var errUnsupportedMedia = errors.New("media operation is not part of this test")
