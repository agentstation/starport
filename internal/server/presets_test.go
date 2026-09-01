package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/apikey"
)

// newPresetTestKey mints one active API key with the given scopes and
// returns its bearer token.
func newPresetTestKey(t *testing.T, server *Server, id string, scopes ...string) string {
	t.Helper()
	token := "test-" + id
	hash := sha256.Sum256([]byte(token))
	_, err := server.apiKeys.Create(context.Background(), apikey.APIKey{
		ID:        id,
		Name:      strings.ReplaceAll(id, "-", "_"),
		Hash:      hex.EncodeToString(hash[:]),
		Scopes:    scopes,
		Active:    true,
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)
	return token
}

func presetJSONRequest(method, path, token, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestPresetCRUDEndpoints(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	writer := newPresetTestKey(t, server, "preset-writer", "presets:write")
	reader := newPresetTestKey(t, server, "preset-reader", "chat:write")

	// A key without presets:write cannot create.
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/presets", reader,
		`{"name":"fast","config":{"model":"mock/test-model"}}`))
	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())

	// Create with the write scope.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/presets", writer,
		`{"name":"fast","description":"cheap default","config":{"model":"mock/test-model","temperature":0.2}}`))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var created struct {
		Name     string `json:"name"`
		Revision uint64 `json:"revision"`
		Config   struct {
			Model       string   `json:"model"`
			Temperature *float32 `json:"temperature"`
		} `json:"config"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &created))
	require.Equal(t, "fast", created.Name)
	require.Equal(t, uint64(1), created.Revision)
	require.Equal(t, "mock/test-model", created.Config.Model)

	// Duplicate create conflicts.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/presets", writer,
		`{"name":"fast","config":{"model":"mock/test-model"}}`))
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())

	// Any authenticated key reads.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodGet, "/api/v1/presets/fast", reader, ""))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodGet, "/api/v1/presets", reader, ""))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var listed struct {
		Data []json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &listed))
	require.Len(t, listed.Data, 1)

	// Updates are revision-checked: a stale revision conflicts, the current
	// revision succeeds and bumps.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPut, "/api/v1/presets/fast", writer,
		`{"config":{"model":"mock/test-model","temperature":0.7},"revision":99}`))
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())

	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPut, "/api/v1/presets/fast", writer,
		`{"config":{"model":"mock/test-model","temperature":0.7},"revision":1}`))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var updated struct {
		Revision uint64 `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &updated))
	require.Equal(t, uint64(2), updated.Revision)

	// A reader cannot delete; the writer can.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodDelete, "/api/v1/presets/fast", reader, ""))
	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())

	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodDelete, "/api/v1/presets/fast", writer, ""))
	require.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())

	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodGet, "/api/v1/presets/fast", reader, ""))
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}

func TestPresetHistoryAndRollbackEndpoints(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	writer := newPresetTestKey(t, server, "preset-history-writer", "presets:write")
	reader := newPresetTestKey(t, server, "preset-history-reader", "chat:write")

	// Two saves build a two-revision history.
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/presets", writer,
		`{"name":"tuned","config":{"model":"mock/test-model","temperature":0.2}}`))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPut, "/api/v1/presets/tuned", writer,
		`{"config":{"model":"mock/test-model","temperature":0.9},"revision":1}`))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	// Any authenticated key reads the history, newest first.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodGet, "/api/v1/presets/tuned/history", reader, ""))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var history struct {
		Data []struct {
			Revision uint64 `json:"revision"`
			Config   struct {
				Temperature *float32 `json:"temperature"`
			} `json:"config"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &history))
	require.Len(t, history.Data, 2)
	require.Equal(t, uint64(2), history.Data[0].Revision)
	require.Equal(t, uint64(1), history.Data[1].Revision)
	require.Equal(t, float32(0.2), *history.Data[1].Config.Temperature)

	// History of an absent preset is a 404.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodGet, "/api/v1/presets/absent/history", reader, ""))
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())

	// A reader cannot roll back.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/presets/tuned/rollback", reader,
		`{"to_revision":1,"revision":2}`))
	require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())

	// A stale expected revision conflicts.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/presets/tuned/rollback", writer,
		`{"to_revision":1,"revision":1}`))
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())

	// Rollback creates revision 3 that copies revision 1.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/presets/tuned/rollback", writer,
		`{"to_revision":1,"revision":2}`))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var rolled struct {
		Revision uint64 `json:"revision"`
		Config   struct {
			Temperature *float32 `json:"temperature"`
		} `json:"config"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &rolled))
	require.Equal(t, uint64(3), rolled.Revision)
	require.Equal(t, float32(0.2), *rolled.Config.Temperature)

	// A rollback to an absent revision is a 404.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/presets/tuned/rollback", writer,
		`{"to_revision":9,"revision":3}`))
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}

func TestChatResolvesPresetReference(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	writer := newPresetTestKey(t, server, "preset-chat-writer", "presets:write", "chat:write")

	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/presets", writer,
		`{"name":"mock-default","config":{"model":"mock/test-model"}}`))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	// The @preset/<name> model reference resolves to the preset's model and
	// completes against the mock provider on both protocol surfaces.
	for _, path := range []string{"/api/v1/chat/completions", "/v1/chat/completions"} {
		recorder = httptest.NewRecorder()
		server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, path, writer,
			`{"model":"@preset/mock-default","messages":[{"role":"user","content":"hello"}]}`))
		require.Equal(t, http.StatusOK, recorder.Code, "%s: %s", path, recorder.Body.String())
	}

	// The OpenRouter preset body field selects the same preset without a
	// model reference.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/chat/completions", writer,
		`{"preset":"mock-default","messages":[{"role":"user","content":"hello"}]}`))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	// An unknown preset is a 404-equivalent protocol error, not a routing 503.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/chat/completions", writer,
		`{"model":"@preset/missing","messages":[{"role":"user","content":"hello"}]}`))
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())

	// A pinned reference resolves the stored revision even after the head
	// moves to a model no provider serves. Only the pin still answers, on
	// both the model field and the body field.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPut, "/api/v1/presets/mock-default", writer,
		`{"config":{"model":"absent/unrouted-model"},"revision":1}`))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/chat/completions", writer,
		`{"model":"@preset/mock-default","messages":[{"role":"user","content":"hello"}]}`))
	require.NotEqual(t, http.StatusOK, recorder.Code, "the moved head no longer routes")

	for _, body := range []string{
		`{"model":"@preset/mock-default@1","messages":[{"role":"user","content":"hello"}]}`,
		`{"preset":"mock-default@1","messages":[{"role":"user","content":"hello"}]}`,
	} {
		recorder = httptest.NewRecorder()
		server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/chat/completions", writer, body))
		require.Equal(t, http.StatusOK, recorder.Code, "%s: %s", body, recorder.Body.String())
	}

	// A pin to an absent revision fails like an unknown preset.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, presetJSONRequest(http.MethodPost, "/api/v1/chat/completions", writer,
		`{"model":"@preset/mock-default@9","messages":[{"role":"user","content":"hello"}]}`))
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}
