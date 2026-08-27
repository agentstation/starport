package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/controllers"
)

// capturingMedia records the canonical request the controller handed the
// gateway, so the test reads what crossed the seam rather than what the wire
// carried.
type capturingMedia struct {
	*mockProxy
	transcription *proxy.TranscriptionRequest
}

func (c *capturingMedia) ProcessTranscription(
	_ context.Context,
	request *proxy.TranscriptionRequest,
) (*proxy.TranscriptionResponse, error) {
	c.transcription = request
	return &proxy.TranscriptionResponse{
		Response: inference.TranscriptionResponse{
			Model:    request.Request.Model,
			Text:     "the recorded sentence",
			Language: "en",
		},
	}, nil
}

// TestTranscriptionUploadReachesTheCanonicalRequest states what a multipart
// body has to become. The audio arrives as a part on the wire and has to reach
// the router as bytes on the canonical request, with the filename the caller
// gave it: a provider reads the extension to decide the container format.
func TestTranscriptionUploadReachesTheCanonicalRequest(t *testing.T) {
	audio := []byte{0x52, 0x49, 0x46, 0x46, 0x24, 0x00, 0x00, 0x00}
	body, contentType := transcriptionForm(t, "standup.wav", audio, map[string]string{
		"model":    "mock/whisper",
		"language": "en",
	})

	service := &capturingMedia{mockProxy: &mockProxy{}}
	controller := controllers.NewMediaController(service)

	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	controller.Transcribe(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotNil(t, service.transcription)

	upload := service.transcription.Request.File
	require.True(t, upload.Present())
	require.Equal(t, "standup.wav", upload.Filename)
	require.Equal(t, audio, upload.Bytes)
	require.Equal(t, "mock/whisper", service.transcription.Request.Model)
	require.Equal(t, "en", service.transcription.Request.Language)
	require.False(t, service.transcription.Request.Translate)

	var answer map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))
	require.Equal(t, "the recorded sentence", answer["text"])
}

// TestTranslationPathMarksTheCanonicalRequest covers the one field the form
// cannot carry. A transcription and a translation send byte-identical bodies,
// so the path the caller reached is the only thing that separates them, and
// the router plans a different operation for each.
func TestTranslationPathMarksTheCanonicalRequest(t *testing.T) {
	body, contentType := transcriptionForm(t, "interview.mp3", []byte{0xff, 0xfb, 0x90}, map[string]string{
		"model": "mock/whisper",
	})

	service := &capturingMedia{mockProxy: &mockProxy{}}
	controller := controllers.NewMediaController(service)

	request := httptest.NewRequest(http.MethodPost, "/v1/audio/translations", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	recorder := httptest.NewRecorder()
	controller.Translate(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NotNil(t, service.transcription)
	require.True(t, service.transcription.Request.Translate)
}

// TestUploadedAudioReplaysAcrossAttempts states the reason the gateway holds
// the bytes rather than the reader. A route plan retries across providers, and
// each attempt builds its own provider request from the canonical one. A
// reader the first attempt drained would reach the second empty.
func TestUploadedAudioReplaysAcrossAttempts(t *testing.T) {
	audio := []byte{0x4f, 0x67, 0x67, 0x53, 0x00, 0x02}
	body, contentType := transcriptionForm(t, "call.ogg", audio, map[string]string{
		"model": "mock/whisper",
	})

	service := &capturingMedia{mockProxy: &mockProxy{}}
	controller := controllers.NewMediaController(service)
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	controller.Transcribe(httptest.NewRecorder(), request)
	require.NotNil(t, service.transcription)

	canonical := service.transcription.Request
	for attempt := 1; attempt <= 3; attempt++ {
		replay := canonical.Clone()
		require.Equal(t, audio, replay.File.Bytes, "attempt %d lost the audio", attempt)
		require.Equal(t, "call.ogg", replay.File.Filename, "attempt %d lost the filename", attempt)
		// A clone that shares its backing array would let one attempt corrupt
		// the next, so the copy has to be its own.
		replay.File.Bytes[0] = 0x00
		require.Equal(t, audio, canonical.File.Bytes)
	}
}

func transcriptionForm(
	t *testing.T,
	filename string,
	audio []byte,
	fields map[string]string,
) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		require.NoError(t, writer.WriteField(name, value))
	}
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(audio)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return body.Bytes(), writer.FormDataContentType()
}
