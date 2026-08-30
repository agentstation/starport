package controllers_test

import (
	"context"
	"errors"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/proxy"
)

// unsupportedOperations answers every operation past chat and embeddings for a
// mock that exercises a different controller. It is the external-test twin of
// the helper in the internal test package, because a mock can only embed a
// type its own package can name.
type unsupportedOperations struct{}

func (unsupportedOperations) ProcessRerank(
	context.Context,
	*proxy.RerankRequest,
) (*proxy.RerankResponse, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperations) ProcessModerations(
	context.Context,
	*proxy.ModerationRequest,
) (*proxy.ModerationResponse, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperations) ProcessImages(
	context.Context,
	*proxy.ImagesRequest,
) (*proxy.ImagesResponse, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperations) ProcessSpeech(
	context.Context,
	*proxy.SpeechRequest,
) (*proxy.SpeechResponse, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperations) ProcessTranscription(
	context.Context,
	*proxy.TranscriptionRequest,
) (*proxy.TranscriptionResponse, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperations) SubmitVideoJob(
	context.Context,
	*proxy.VideoSubmitRequest,
) (*proxy.VideoJobAnswer, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperations) PollVideoJob(
	context.Context,
	*proxy.VideoJobRequest,
) (*proxy.VideoJobAnswer, error) {
	return nil, errUnsupportedOperation
}

// VideoJobRunner answers no provider side, so a record store handed this mock
// refuses to start work rather than starting work nothing serves.
func (unsupportedOperations) VideoJobRunner(*proxy.VideoSubmitRequest) jobs.Runner {
	return nil
}

func (unsupportedOperations) CancelVideoJob(
	context.Context,
	*proxy.VideoJobRequest,
) (*proxy.VideoJobAnswer, error) {
	return nil, errUnsupportedOperation
}

func (unsupportedOperations) FetchVideoAsset(
	context.Context,
	*proxy.VideoAssetRequest,
) (*proxy.VideoAsset, error) {
	return nil, errUnsupportedOperation
}

// errUnsupportedOperation is the answer a mock without a media path gives.
var errUnsupportedOperation = errors.New("media operation is not part of this test")
