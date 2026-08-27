// Package mediaform reads one media request that arrived as multipart form
// data. An image edit and an audio transcription carry a file, so neither can
// arrive as JSON. Both protocol families spell the parts the same way, so the
// reading is owned once here rather than copied into each codec, where two
// copies would drift into two contracts for one wire format.
package mediaform

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"strconv"

	"github.com/agentstation/starport/internal/inference"
)

// ErrFormRequired reports a call that reached a multipart path without a
// parsed form.
var ErrFormRequired = errors.New("multipart form is required")

// ErrUploadUnreadable reports a part the gateway could not read.
var ErrUploadUnreadable = errors.New("upload could not be read")

// Images reads one image edit request. The source image is what separates an
// edit from a generation, so a form carrying none reads as a generation and
// the router plans it as one.
func Images(form *multipart.Form) (inference.ImagesRequest, error) {
	if form == nil {
		return inference.ImagesRequest{}, ErrFormRequired
	}
	image, err := File(form, "image")
	if err != nil {
		return inference.ImagesRequest{}, err
	}
	mask, err := File(form, "mask")
	if err != nil {
		return inference.ImagesRequest{}, err
	}
	return inference.ImagesRequest{
		Model:          Value(form, "model"),
		Prompt:         Value(form, "prompt"),
		N:              Int(form, "n"),
		Size:           Value(form, "size"),
		Quality:        Value(form, "quality"),
		Style:          Value(form, "style"),
		ResponseFormat: Value(form, "response_format"),
		User:           Value(form, "user"),
		Image:          image,
		Mask:           mask,
	}, nil
}

// Transcription reads one speech-to-text request. translate states whether the
// caller reached the translation path, which is a routing fact and not a form
// field.
func Transcription(form *multipart.Form, translate bool) (inference.TranscriptionRequest, error) {
	if form == nil {
		return inference.TranscriptionRequest{}, ErrFormRequired
	}
	file, err := File(form, "file")
	if err != nil {
		return inference.TranscriptionRequest{}, err
	}
	return inference.TranscriptionRequest{
		Model:          Value(form, "model"),
		File:           file,
		Language:       Value(form, "language"),
		Prompt:         Value(form, "prompt"),
		ResponseFormat: Value(form, "response_format"),
		Temperature:    Float(form, "temperature"),
		Translate:      translate,
	}, nil
}

// File reads one named upload whole. The bytes are held rather than streamed
// because a route plan retries across providers, and a reader consumed by the
// first attempt would arrive empty at the second.
func File(form *multipart.Form, field string) (inference.UploadedFile, error) {
	headers := form.File[field]
	if len(headers) == 0 {
		return inference.UploadedFile{}, nil
	}
	header := headers[0]
	opened, err := header.Open()
	if err != nil {
		return inference.UploadedFile{}, fmt.Errorf("%w: %s: %w", ErrUploadUnreadable, field, err)
	}
	defer func() { _ = opened.Close() }()
	bytes, err := io.ReadAll(opened)
	if err != nil {
		return inference.UploadedFile{}, fmt.Errorf("%w: %s: %w", ErrUploadUnreadable, field, err)
	}
	return inference.UploadedFile{
		Filename:  header.Filename,
		MediaType: header.Header.Get("Content-Type"),
		Bytes:     bytes,
	}, nil
}

// Value reads one text field, or the empty string when the form carries none.
func Value(form *multipart.Form, field string) string {
	values := form.Value[field]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Int reads one whole-number field. An unparsable value reads as zero, which
// the validator then reports by name rather than this reader guessing at it.
func Int(form *multipart.Form, field string) int {
	parsed, err := strconv.Atoi(Value(form, field))
	if err != nil {
		return 0
	}
	return parsed
}

// Float reads one optional decimal field. A field the form omits stays nil,
// which is not the same as zero: a provider reads nil as its own default.
func Float(form *multipart.Form, field string) *float64 {
	raw := Value(form, field)
	if raw == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &parsed
}
