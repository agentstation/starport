package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/agentstation/starport/internal/inference"
)

// A batch crosses the wire three ways: a create request, a batch object a
// caller polls, and the JSONL lines inside the input and result files. The
// codec owns every wire word for all three, so the batch runner and the
// controller never spell a JSON name.

const (
	// BatchCompletionWindow is the one window OpenAI publishes. A gateway a
	// developer runs starts the work at once, so the window is a promise the
	// gateway keeps trivially, and any other value is a request it cannot
	// honor the way the caller means it.
	BatchCompletionWindow = "24h"

	// batchLineMethod is the one method a batch line may name. Every batch
	// endpoint is a POST route.
	batchLineMethod = "POST"
)

// BatchEndpoints lists the operation paths a batch may call, which are the
// request-shaped operations this gateway serves: chat, embeddings, and
// responses. A media operation stays online, because its result is bytes
// rather than a JSON body a result line can carry.
func BatchEndpoints() []string {
	return []string{"/v1/chat/completions", "/v1/embeddings", "/v1/responses"}
}

// BatchCreateRequest is the OpenAI batch creation wire request.
type BatchCreateRequest struct {
	InputFileID      string `json:"input_file_id"`
	Endpoint         string `json:"endpoint"`
	CompletionWindow string `json:"completion_window,omitempty"`
	// Metadata is accepted so a stock SDK call decodes, and it is not
	// stored: this gateway keeps no free-form annotations on a batch.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// DecodeBatchCreate decodes one strict OpenAI batch creation request.
func DecodeBatchCreate(reader io.Reader) (inference.BatchCreateRequest, error) {
	var wire BatchCreateRequest
	if err := decodeStrict(reader, &wire); err != nil {
		return inference.BatchCreateRequest{}, err
	}
	if strings.TrimSpace(wire.InputFileID) == "" {
		return inference.BatchCreateRequest{}, fmt.Errorf("input_file_id is required")
	}
	endpoint := strings.TrimSpace(wire.Endpoint)
	if !batchEndpointServed(endpoint) {
		return inference.BatchCreateRequest{}, fmt.Errorf(
			"endpoint must be one of %s", strings.Join(BatchEndpoints(), ", "),
		)
	}
	window := strings.TrimSpace(wire.CompletionWindow)
	if window == "" {
		window = BatchCompletionWindow
	}
	if window != BatchCompletionWindow {
		return inference.BatchCreateRequest{}, fmt.Errorf(
			"completion_window must be %q", BatchCompletionWindow,
		)
	}
	return inference.BatchCreateRequest{
		InputFileID:      wire.InputFileID,
		Endpoint:         endpoint,
		CompletionWindow: window,
	}, nil
}

func batchEndpointServed(endpoint string) bool {
	for _, served := range BatchEndpoints() {
		if endpoint == served {
			return true
		}
	}
	return false
}

// Batch is the OpenAI batch wire object.
type Batch struct {
	ID               string             `json:"id"`
	Object           string             `json:"object"`
	Endpoint         string             `json:"endpoint"`
	InputFileID      string             `json:"input_file_id"`
	CompletionWindow string             `json:"completion_window"`
	Status           string             `json:"status"`
	OutputFileID     string             `json:"output_file_id,omitempty"`
	ErrorFileID      string             `json:"error_file_id,omitempty"`
	CreatedAt        int64              `json:"created_at"`
	CompletedAt      int64              `json:"completed_at,omitempty"`
	FailedAt         int64              `json:"failed_at,omitempty"`
	CancelledAt      int64              `json:"cancelled_at,omitempty"`
	RequestCounts    BatchRequestCounts `json:"request_counts"`
	Errors           *BatchErrors       `json:"errors,omitempty"`
}

// BatchRequestCounts is the wire progress summary.
type BatchRequestCounts struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

// BatchErrors states why a failed batch stopped before its lines did.
type BatchErrors struct {
	Object string       `json:"object"`
	Data   []BatchError `json:"data"`
}

// BatchError is one entry inside BatchErrors.
type BatchError struct {
	Message string `json:"message"`
}

// BatchList is the OpenAI listing of one caller's batches.
type BatchList struct {
	Object  string  `json:"object"`
	Data    []Batch `json:"data"`
	HasMore bool    `json:"has_more"`
}

// EncodeBatch converts one canonical batch to OpenAI wire values.
func EncodeBatch(batch inference.Batch) Batch {
	wire := Batch{
		ID:               batch.ID,
		Object:           "batch",
		Endpoint:         batch.Endpoint,
		InputFileID:      batch.InputFileID,
		CompletionWindow: BatchCompletionWindow,
		Status:           batchStatus(batch.State),
		OutputFileID:     batch.OutputFileID,
		ErrorFileID:      batch.ErrorFileID,
		CreatedAt:        batch.CreatedUnix,
		RequestCounts: BatchRequestCounts{
			Total:     batch.TotalLines,
			Completed: batch.CompletedLines,
			Failed:    batch.FailedLines,
		},
	}
	switch batch.State {
	case "completed":
		wire.CompletedAt = batch.CompletedUnix
	case "failed":
		wire.FailedAt = batch.CompletedUnix
	case "cancelled":
		wire.CancelledAt = batch.CompletedUnix
	}
	if batch.Reason != "" {
		wire.Errors = &BatchErrors{Object: ListObject, Data: []BatchError{{Message: batch.Reason}}}
	}
	return wire
}

// EncodeBatches converts one listing to OpenAI wire values.
func EncodeBatches(records []inference.Batch) BatchList {
	data := make([]Batch, len(records))
	for index, batch := range records {
		data[index] = EncodeBatch(batch)
	}
	return BatchList{Object: ListObject, Data: data}
}

// batchStatus renders the canonical state as the word this family publishes.
// OpenAI names a queued batch validating and a working one in_progress. The
// three terminal words match the canonical ones.
func batchStatus(state string) string {
	switch state {
	case "queued":
		return "validating"
	case "running":
		return "in_progress"
	}
	return state
}

// BatchLineRequest is one wire line of a batch input file.
type BatchLineRequest struct {
	CustomID string          `json:"custom_id"`
	Method   string          `json:"method"`
	URL      string          `json:"url"`
	Body     json.RawMessage `json:"body"`
}

// DecodeBatchLine decodes one strict input line and checks it against the
// batch's endpoint. A line that names another endpoint fails here, before any
// provider call, because one batch serves one operation.
func DecodeBatchLine(line []byte, endpoint string) (inference.BatchLine, error) {
	var wire BatchLineRequest
	if err := decodeStrict(bytes.NewReader(line), &wire); err != nil {
		return inference.BatchLine{}, err
	}
	switch {
	case strings.TrimSpace(wire.CustomID) == "":
		return inference.BatchLine{}, fmt.Errorf("custom_id is required")
	case wire.Method != batchLineMethod:
		return inference.BatchLine{}, fmt.Errorf("method must be %s", batchLineMethod)
	case wire.URL != endpoint:
		return inference.BatchLine{}, fmt.Errorf("url must match the batch endpoint %s", endpoint)
	case len(bytes.TrimSpace(wire.Body)) == 0:
		return inference.BatchLine{}, fmt.Errorf("body is required")
	}
	return inference.BatchLine{CustomID: wire.CustomID, URL: wire.URL, Body: wire.Body}, nil
}

// BatchLineResponse is one wire line of a batch output or error file.
type BatchLineResponse struct {
	ID       string            `json:"id"`
	CustomID string            `json:"custom_id"`
	Response *BatchLineAnswer  `json:"response"`
	Error    *BatchLineFailure `json:"error"`
}

// BatchLineAnswer carries what the online route would have answered.
type BatchLineAnswer struct {
	StatusCode int             `json:"status_code"`
	RequestID  string          `json:"request_id,omitempty"`
	Body       json.RawMessage `json:"body"`
}

// BatchLineFailure mirrors the OpenAI per-line error slot. This gateway
// reports every failure through the response body instead, the way the online
// route does, so the slot encodes as null.
type BatchLineFailure struct {
	Message string `json:"message"`
}

// EncodeBatchOutputLine converts one line result to its wire line. The same
// shape serves the output file and the error file: which file the line landed
// in already says whether it failed, and the status code repeats it.
func EncodeBatchOutputLine(result inference.BatchLineResult) ([]byte, error) {
	line := BatchLineResponse{
		ID:       "batch_req_" + result.RequestID,
		CustomID: result.CustomID,
		Response: &BatchLineAnswer{
			StatusCode: result.StatusCode,
			RequestID:  result.RequestID,
			Body:       json.RawMessage(result.Body),
		},
	}
	encoded, err := json.Marshal(line)
	if err != nil {
		return nil, fmt.Errorf("encode batch result line: %w", err)
	}
	return encoded, nil
}
