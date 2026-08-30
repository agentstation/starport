package openai_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/protocol/openai"
)

// TestDecodeBatchCreateReadsTheWireNames proves the create codec by key. An
// SDK writes these exact names, so a renamed field breaks a client Starport
// never sees.
func TestDecodeBatchCreateReadsTheWireNames(t *testing.T) {
	t.Parallel()

	decoded, err := openai.DecodeBatchCreate(strings.NewReader(
		`{"input_file_id":"file-1","endpoint":"/v1/chat/completions",` +
			`"completion_window":"24h","metadata":{"team":"search"}}`))
	require.NoError(t, err)
	require.Equal(t, "file-1", decoded.InputFileID)
	require.Equal(t, "/v1/chat/completions", decoded.Endpoint)
	require.Equal(t, "24h", decoded.CompletionWindow)
}

// TestDecodeBatchCreateFillsTheOnlyWindow covers the caller who omits the
// window. There is one publishable value, so the codec supplies it.
func TestDecodeBatchCreateFillsTheOnlyWindow(t *testing.T) {
	t.Parallel()

	decoded, err := openai.DecodeBatchCreate(strings.NewReader(
		`{"input_file_id":"file-1","endpoint":"/v1/embeddings"}`))
	require.NoError(t, err)
	require.Equal(t, openai.BatchCompletionWindow, decoded.CompletionWindow)
}

// TestDecodeBatchCreateRefusesBadInput names each refusal the codec owns: a
// missing file, an endpoint outside the served set, another window, and a key
// the wire shape does not define.
func TestDecodeBatchCreateRefusesBadInput(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		body string
		want string
	}{
		"missing file": {
			body: `{"endpoint":"/v1/chat/completions"}`,
			want: "input_file_id",
		},
		"unserved endpoint": {
			body: `{"input_file_id":"file-1","endpoint":"/v1/images/generations"}`,
			want: "endpoint",
		},
		"other window": {
			body: `{"input_file_id":"file-1","endpoint":"/v1/responses","completion_window":"7d"}`,
			want: "completion_window",
		},
		"unknown key": {
			body: `{"input_file_id":"file-1","endpoint":"/v1/responses","priority":"high"}`,
			want: "priority",
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := openai.DecodeBatchCreate(strings.NewReader(test.body))
			require.Error(t, err)
			require.Contains(t, err.Error(), test.want)
		})
	}
}

// TestBatchEndpointsAreTheRequestShapedSurface pins the served set. A media
// operation answers bytes, which a JSONL result line cannot carry, so the
// list holds exactly the three JSON-answering operations.
func TestBatchEndpointsAreTheRequestShapedSurface(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		[]string{"/v1/chat/completions", "/v1/embeddings", "/v1/responses"},
		openai.BatchEndpoints())
}

// TestEncodeBatchWritesEveryWireKey proves the poll object by key, the same
// way the stored-file test does.
func TestEncodeBatchWritesEveryWireKey(t *testing.T) {
	t.Parallel()

	batch := openai.EncodeBatch(inferenceBatch())
	var decoded map[string]any
	require.NoError(t, roundTrip(t, batch, &decoded))

	require.Equal(t, map[string]any{
		"id":                "batch-1",
		"object":            "batch",
		"endpoint":          "/v1/chat/completions",
		"input_file_id":     "file-in",
		"completion_window": "24h",
		"status":            "completed",
		"output_file_id":    "file-out",
		"error_file_id":     "file-err",
		"created_at":        float64(1756252800),
		"completed_at":      float64(1756256400),
		"request_counts": map[string]any{
			"total":     float64(3),
			"completed": float64(2),
			"failed":    float64(1),
		},
	}, decoded)
}

// TestEncodeBatchRendersTheFamilyStatusWords holds the state mapping. This
// family names a queued batch validating and a working one in_progress, and
// the terminal words pass through. A failed or cancelled batch stamps its
// own timestamp key, because a client reads the state from whichever key is
// present as much as from the status word.
func TestEncodeBatchRendersTheFamilyStatusWords(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		state    string
		status   string
		stampKey string
	}{
		"queued":    {state: "queued", status: "validating"},
		"running":   {state: "running", status: "in_progress"},
		"completed": {state: "completed", status: "completed", stampKey: "completed_at"},
		"failed":    {state: "failed", status: "failed", stampKey: "failed_at"},
		"cancelled": {state: "cancelled", status: "cancelled", stampKey: "cancelled_at"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			record := inferenceBatch()
			record.State = test.state
			var decoded map[string]any
			require.NoError(t, roundTrip(t, openai.EncodeBatch(record), &decoded))
			require.Equal(t, test.status, decoded["status"])
			for _, key := range []string{"completed_at", "failed_at", "cancelled_at"} {
				if key == test.stampKey {
					require.Contains(t, decoded, key)
				} else {
					require.NotContains(t, decoded, key)
				}
			}
		})
	}
}

// TestEncodeBatchCarriesAStopReason covers the batch that stopped before its
// lines did. The reason rides the errors slot, so a client that reads only
// this object learns why.
func TestEncodeBatchCarriesAStopReason(t *testing.T) {
	t.Parallel()

	record := inferenceBatch()
	record.State = "failed"
	record.Reason = "line 4 exceeds the 4194304 byte line bound"
	var decoded map[string]any
	require.NoError(t, roundTrip(t, openai.EncodeBatch(record), &decoded))

	errors, ok := decoded["errors"].(map[string]any)
	require.True(t, ok, "errors slot is absent")
	data, ok := errors["data"].([]any)
	require.True(t, ok)
	require.Len(t, data, 1)
	entry, ok := data[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, record.Reason, entry["message"])
}

// TestDecodeBatchLineChecksEveryRule names the per-line refusals: the id, the
// method, the endpoint match, and the body.
func TestDecodeBatchLineChecksEveryRule(t *testing.T) {
	t.Parallel()

	line, err := openai.DecodeBatchLine([]byte(
		`{"custom_id":"a","method":"POST","url":"/v1/embeddings","body":{"model":"m","input":"x"}}`),
		"/v1/embeddings")
	require.NoError(t, err)
	require.Equal(t, "a", line.CustomID)
	require.Equal(t, "/v1/embeddings", line.URL)
	require.JSONEq(t, `{"model":"m","input":"x"}`, string(line.Body))

	cases := map[string]struct {
		line string
		want string
	}{
		"missing custom_id": {
			line: `{"method":"POST","url":"/v1/embeddings","body":{}}`,
			want: "custom_id",
		},
		"wrong method": {
			line: `{"custom_id":"a","method":"GET","url":"/v1/embeddings","body":{}}`,
			want: "method",
		},
		"other endpoint": {
			line: `{"custom_id":"a","method":"POST","url":"/v1/chat/completions","body":{}}`,
			want: "url",
		},
		"missing body": {
			line: `{"custom_id":"a","method":"POST","url":"/v1/embeddings"}`,
			want: "body",
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := openai.DecodeBatchLine([]byte(test.line), "/v1/embeddings")
			require.Error(t, err)
			require.Contains(t, err.Error(), test.want)
		})
	}
}

// TestEncodeBatchOutputLineWritesTheWireLine proves the result line by key.
// The line id carries the batch_req_ prefix over the per-line request id, the
// response slot carries the online answer, and the error slot encodes null,
// because this gateway reports every failure through the response body the
// way the online route does.
func TestEncodeBatchOutputLineWritesTheWireLine(t *testing.T) {
	t.Parallel()

	encoded, err := openai.EncodeBatchOutputLine(inference.BatchLineResult{
		CustomID:   "line-1",
		StatusCode: 200,
		RequestID:  "req-abc",
		Body:       []byte(`{"object":"chat.completion"}`),
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, "batch_req_req-abc", decoded["id"])
	require.Equal(t, "line-1", decoded["custom_id"])
	require.Contains(t, decoded, "error")
	require.Nil(t, decoded["error"])

	response, ok := decoded["response"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(200), response["status_code"])
	require.Equal(t, "req-abc", response["request_id"])
	require.Equal(t, map[string]any{"object": "chat.completion"}, response["body"])
}

// inferenceBatch is one fully populated canonical record for the encode
// tests.
func inferenceBatch() inference.Batch {
	return inference.Batch{
		ID:             "batch-1",
		Endpoint:       "/v1/chat/completions",
		InputFileID:    "file-in",
		OutputFileID:   "file-out",
		ErrorFileID:    "file-err",
		State:          "completed",
		TotalLines:     3,
		CompletedLines: 2,
		FailedLines:    1,
		CreatedUnix:    1756252800,
		CompletedUnix:  1756256400,
	}
}
