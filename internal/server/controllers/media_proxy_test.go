package controllers

import (
	"context"
	"errors"

	"github.com/agentstation/starport/internal/proxy"
)

// unsupportedMedia answers the three media operations for a mock that
// exercises a different controller. A mock gains them by embedding it, so
// adding a media operation to proxy.Proxy does not edit every mock here.
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
