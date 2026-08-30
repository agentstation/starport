package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

// batchesPath is where the OpenAI dialect mounts the surface. OpenRouter
// publishes no batch route, so the OpenAI family serves it alone.
const batchesPath = "/v1/batches"

// TestServerRegistersTheBatchPaths walks the router the server builds. A path
// spelled correctly in the source and mounted under the wrong group reads as
// present to a source scan and answers 404 to a caller.
func TestServerRegistersTheBatchPaths(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})

	registered := map[string]bool{}
	walk := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[method+" "+strings.TrimSuffix(route, "/")] = true
		return nil
	}
	require.NoError(t, chi.Walk(server.router, walk))
	for _, route := range []string{
		"POST " + batchesPath,
		"GET " + batchesPath,
		"GET " + batchesPath + "/{batch_id}",
		"POST " + batchesPath + "/{batch_id}/cancel",
	} {
		require.True(t, registered[route], "%s is not registered", route)
	}
}

// TestBatchesCarryTheirOwnScope states the access rule. A batch spends the
// account's money on many requests at once, so a key that writes chat holds
// no batch access until an operator grants it.
func TestBatchesCarryTheirOwnScope(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	chatOnly := storeMediaTestKey(t, server, "batches-chat-only", "chat:write", "files:write")
	batchKey := storeMediaTestKey(t, server, "batches-writer", "batches:write")

	refused := postMediaRequest(server, batchesPath, chatOnly)
	require.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())

	// An empty JSON body reaches the handler and fails at the codec, which
	// names no input file. That answer proves the controller ran rather than
	// the scope guard.
	allowed := postMediaRequest(server, batchesPath, batchKey)
	require.Equal(t, http.StatusBadRequest, allowed.Code, allowed.Body.String())
	require.Contains(t, allowed.Body.String(), "input_file_id")
}

// TestAnonymousDeploymentReachesTheBatchRoutes covers the operator who runs
// with authentication disabled. The anonymous key has to carry the scope, or
// the mode that exists to make the first request work would refuse this one.
func TestAnonymousDeploymentReachesTheBatchRoutes(t *testing.T) {
	config := unauthenticatedConfig()
	config.MaxRequestSize = 1 << 20
	server := newTestServer(t, config)

	recorder := postMediaRequest(server, batchesPath, "")
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
}

// TestBatchCreateRefusesAMissingInputFile covers the caller who names a file
// that does not exist, or one another account owns. Both read the same way.
func TestBatchCreateRefusesAMissingInputFile(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	key := storeMediaTestKey(t, server, "batches-no-file", "batches:write")

	recorder := postBatchJSON(server, batchesPath, key,
		`{"input_file_id":"file-missing","endpoint":"/v1/chat/completions"}`)
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "input_file_id")
}

// TestBatchCreateRefusesTheWrongPurpose covers a real stored file uploaded
// for another use. The batch surface reads only files uploaded for it.
func TestBatchCreateRefusesTheWrongPurpose(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	key := storeMediaTestKey(t, server, "batches-wrong-purpose",
		"batches:write", "files:write")
	fileID := uploadBatchTestFile(t, server, key, "user_data", "{}\n")

	recorder := postBatchJSON(server, batchesPath, key,
		`{"input_file_id":"`+fileID+`","endpoint":"/v1/chat/completions"}`)
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "purpose")
}

// TestBatchRunsToATerminalRecordThroughTheRouter is the HTTP acceptance pass.
// A three-line file rides the whole surface: upload, create, poll to the
// terminal record, and read the output file back. The mock connector serves
// every line, so the record reaches completed with three responses in the
// output file, each carrying the online chat envelope under its custom_id.
func TestBatchRunsToATerminalRecordThroughTheRouter(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	key := storeMediaTestKey(t, server, "batches-run",
		"batches:write", "files:write", "files:read")

	input := `{"custom_id":"line-1","method":"POST","url":"/v1/chat/completions","body":{"model":"mock/model","messages":[{"role":"user","content":"one"}]}}` + "\n" +
		`{"custom_id":"line-2","method":"POST","url":"/v1/chat/completions","body":{"model":"mock/model","messages":[{"role":"user","content":"two"}]}}` + "\n" +
		`{"custom_id":"line-3","method":"POST","url":"/v1/chat/completions","body":{"model":"mock/model","messages":[{"role":"user","content":"three"}]}}` + "\n"
	fileID := uploadBatchTestFile(t, server, key, "batch", input)

	created := postBatchJSON(server, batchesPath, key,
		`{"input_file_id":"`+fileID+`","endpoint":"/v1/chat/completions","completion_window":"24h"}`)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())

	var submitted struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &submitted))
	require.NotEmpty(t, submitted.ID)
	require.Contains(t, []string{"validating", "in_progress"}, submitted.Status)

	var final struct {
		Status        string `json:"status"`
		OutputFileID  string `json:"output_file_id"`
		ErrorFileID   string `json:"error_file_id"`
		RequestCounts struct {
			Total     int `json:"total"`
			Completed int `json:"completed"`
			Failed    int `json:"failed"`
		} `json:"request_counts"`
	}
	require.Eventually(t, func() bool {
		recorder := getBatchRequest(server, batchesPath+"/"+submitted.ID, key)
		if recorder.Code != http.StatusOK {
			return false
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &final); err != nil {
			return false
		}
		return final.Status == "completed"
	}, 10*time.Second, 20*time.Millisecond)

	require.Equal(t, 3, final.RequestCounts.Total)
	require.Equal(t, 3, final.RequestCounts.Completed)
	require.Equal(t, 0, final.RequestCounts.Failed)
	require.NotEmpty(t, final.OutputFileID)
	require.Empty(t, final.ErrorFileID)

	content := getBatchRequest(server, "/v1/files/"+final.OutputFileID+"/content", key)
	require.Equal(t, http.StatusOK, content.Code, content.Body.String())
	lines := strings.Split(strings.TrimSpace(content.Body.String()), "\n")
	require.Len(t, lines, 3)
	seen := map[string]bool{}
	for _, line := range lines {
		var result struct {
			ID       string `json:"id"`
			CustomID string `json:"custom_id"`
			Response struct {
				StatusCode int             `json:"status_code"`
				Body       json.RawMessage `json:"body"`
			} `json:"response"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &result))
		require.True(t, strings.HasPrefix(result.ID, "batch_req_"), result.ID)
		seen[result.CustomID] = true
		require.Equal(t, http.StatusOK, result.Response.StatusCode)
		require.Contains(t, string(result.Response.Body), "chat.completion")
	}
	require.Len(t, seen, 3)

	// The batch ended, so a cancel now reads as the conflict it is.
	cancelled := postBatchJSON(server, batchesPath+"/"+submitted.ID+"/cancel", key, "")
	require.Equal(t, http.StatusConflict, cancelled.Code, cancelled.Body.String())
}

// TestBatchGetAnswersNotFoundForAnUnknownIdentifier covers the poll of an
// identifier this account does not hold.
func TestBatchGetAnswersNotFoundForAnUnknownIdentifier(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	key := storeMediaTestKey(t, server, "batches-unknown", "batches:write")

	recorder := getBatchRequest(server, batchesPath+"/batch-unknown", key)
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "No such Batch object")
}

// TestFileUploadRefusesTheBatchOutputPurpose guards the reserved purpose. The
// batch runner is the only writer of an output file, so an upload that claims
// the purpose is refused before it stores anything.
func TestFileUploadRefusesTheBatchOutputPurpose(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	key := storeMediaTestKey(t, server, "batches-output-upload", "files:write")

	recorder := postBatchMultipart(t, server, key, "batch_output", "{}\n")
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "purpose")
}

// uploadBatchTestFile stores one file through the upload route and answers
// its identifier.
func uploadBatchTestFile(t *testing.T, server *Server, key, purpose, content string) string {
	t.Helper()
	recorder := postBatchMultipart(t, server, key, purpose, content)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var stored struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &stored))
	require.NotEmpty(t, stored.ID)
	return stored.ID
}

func postBatchMultipart(t *testing.T, server *Server, key, purpose, content string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	require.NoError(t, form.WriteField("purpose", purpose))
	part, err := form.CreateFormFile("file", "input.jsonl")
	require.NoError(t, err)
	_, err = part.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, form.Close())

	request := httptest.NewRequest(http.MethodPost, "/v1/files", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)
	return recorder
}

func postBatchJSON(server *Server, path, key, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)
	return recorder
}

func getBatchRequest(server *Server, path, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)
	return recorder
}
