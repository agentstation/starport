package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

// multipartFileField is the form field name an OpenAI-compatible audio
// endpoint reads the upload from. It shares its spelling with the chat content
// part type and nothing else, so the two stay separate constants.
const multipartFileField = "file"

// setHeadersFunc applies provider request authentication.
type setHeadersFunc func(credentials.Material, *http.Request) error

// handleErrorFunc converts a provider rejection into an *APIError, so a media
// failure reaches internal/failure through the same normalization the chat
// path uses.
type handleErrorFunc func(*http.Response) error

// GenerateImages performs an image generation or image edit call against an
// OpenAI-compatible API. The selected endpoint decides which one, and the
// presence of a source image decides the encoding: a generation sends JSON and
// an edit sends multipart form data, which is what the wire contract states.
func (c *OpenAICompatibleConnector) GenerateImages(
	ctx context.Context,
	req *ImagesRequest,
	setHeaders setHeadersFunc,
	handleError handleErrorFunc,
) (*ImagesResponse, error) {
	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeOpenAI)
	if err != nil {
		return nil, err
	}
	var httpReq *http.Request
	if req.Image.Present() {
		httpReq, err = imageEditRequest(ctx, endpoint, req)
	} else {
		httpReq, err = jsonRequest(ctx, endpoint, req)
	}
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, httpReq, req.Credential, setHeaders, handleError)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var decoded ImagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &decoded, nil
}

// SynthesizeSpeech performs a text-to-speech call. A speech endpoint answers
// with the encoded audio file rather than with JSON, so the response body is
// read whole and its media type is kept.
func (c *OpenAICompatibleConnector) SynthesizeSpeech(
	ctx context.Context,
	req *SpeechRequest,
	setHeaders setHeadersFunc,
	handleError handleErrorFunc,
) (*SpeechResponse, error) {
	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeOpenAI)
	if err != nil {
		return nil, err
	}
	httpReq, err := jsonRequest(ctx, endpoint, req)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, httpReq, req.Credential, setHeaders, handleError)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read audio response: %w", err)
	}
	return &SpeechResponse{Audio: audio, ContentType: resp.Header.Get("Content-Type")}, nil
}

// Transcribe performs a speech-to-text call. One method serves transcription
// and translation, because the two differ only in the endpoint the catalog
// names for the operation.
func (c *OpenAICompatibleConnector) Transcribe(
	ctx context.Context,
	req *TranscriptionRequest,
	setHeaders setHeadersFunc,
	handleError handleErrorFunc,
) (*TranscriptionResponse, error) {
	if !req.File.Present() {
		return nil, fmt.Errorf("%w: transcription carries no audio file", ErrInvalidMediaRequest)
	}
	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeOpenAI)
	if err != nil {
		return nil, err
	}
	fields := map[string]string{"model": req.Model}
	addField(fields, "language", req.Language)
	addField(fields, "prompt", req.Prompt)
	addField(fields, "response_format", req.ResponseFormat)
	if req.Temperature != nil {
		fields["temperature"] = strconv.FormatFloat(*req.Temperature, 'f', -1, 64)
	}
	httpReq, err := multipartRequest(ctx, endpoint, fields, map[string]UploadedFile{multipartFileField: req.File})
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, httpReq, req.Credential, setHeaders, handleError)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read transcription response: %w", err)
	}
	var decoded TranscriptionResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		// A caller that asked for text, srt, or vtt receives the transcript
		// itself rather than a JSON object. Keeping the body is the honest
		// answer, because the provider did exactly what the caller asked.
		return &TranscriptionResponse{Text: string(body)}, nil
	}
	return &decoded, nil
}

// send applies authentication, performs the call, and converts a rejection
// through the shared error handler. Every media method uses it, so no media
// path can grow its own error vocabulary.
func (c *OpenAICompatibleConnector) send(
	_ context.Context,
	httpReq *http.Request,
	credential credentials.Material,
	setHeaders setHeadersFunc,
	handleError handleErrorFunc,
) (*http.Response, error) {
	if err := setHeaders(credential, httpReq); err != nil {
		return nil, fmt.Errorf("apply provider request authentication: %w", err)
	}
	resp, err := doRequest(c.httpClient, httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return nil, handleError(resp)
	}
	return resp, nil
}

func jsonRequest(ctx context.Context, endpoint string, payload any) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

func imageEditRequest(
	ctx context.Context,
	endpoint string,
	req *ImagesRequest,
) (*http.Request, error) {
	fields := map[string]string{"model": req.Model, "prompt": req.Prompt}
	addField(fields, "size", req.Size)
	addField(fields, "quality", req.Quality)
	addField(fields, "style", req.Style)
	addField(fields, "response_format", req.ResponseFormat)
	addField(fields, "user", req.User)
	if req.N > 0 {
		fields["n"] = strconv.Itoa(req.N)
	}
	files := map[string]UploadedFile{"image": req.Image}
	if req.Mask.Present() {
		files["mask"] = req.Mask
	}
	return multipartRequest(ctx, endpoint, fields, files)
}

func multipartRequest(
	ctx context.Context,
	endpoint string,
	fields map[string]string,
	files map[string]UploadedFile,
) (*http.Request, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, name := range sortedFieldNames(fields) {
		if err := writer.WriteField(name, fields[name]); err != nil {
			return nil, fmt.Errorf("failed to write %s field: %w", name, err)
		}
	}
	for _, name := range sortedFieldNames(files) {
		upload := files[name]
		filename := upload.Filename
		if filename == "" {
			filename = name
		}
		part, err := writer.CreateFormFile(name, filename)
		if err != nil {
			return nil, fmt.Errorf("failed to write %s upload: %w", name, err)
		}
		if _, err := part.Write(upload.Bytes); err != nil {
			return nil, fmt.Errorf("failed to write %s upload: %w", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart body: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	return httpReq, nil
}

// sortedFieldNames keeps a multipart body byte-identical across runs, so a
// retry sends the same bytes and a test can assert on them.
func sortedFieldNames[Value any](values map[string]Value) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func addField(fields map[string]string, name, value string) {
	if value != "" {
		fields[name] = value
	}
}
