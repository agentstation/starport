package controllers_test

import (
	"context"
	"errors"

	"github.com/agentstation/starport/internal/proxy"
)

// unsupportedMedia answers the three media operations for a mock that
// exercises a different controller. It is the external-test twin of the
// helper in the internal test package, because a mock can only embed a type
// its own package can name.
type unsupportedMedia struct{}

func (unsupportedMedia) ProcessImages(
	context.Context,
	*proxy.ImagesRequest,
) (*proxy.ImagesResponse, error) {
	return nil, errUnsupportedMedia
}

func (unsupportedMedia) ProcessSpeech(
	context.Context,
	*proxy.SpeechRequest,
) (*proxy.SpeechResponse, error) {
	return nil, errUnsupportedMedia
}

func (unsupportedMedia) ProcessTranscription(
	context.Context,
	*proxy.TranscriptionRequest,
) (*proxy.TranscriptionResponse, error) {
	return nil, errUnsupportedMedia
}

// errUnsupportedMedia is the answer a mock without a media path gives.
var errUnsupportedMedia = errors.New("media operation is not part of this test")
