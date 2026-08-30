package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingEventEmitter keeps every event a controller pushes. It records
// the whole payload rather than a count, because the property under test
// includes what a webhook receiver must never read.
type recordingEventEmitter struct {
	types    []string
	payloads []map[string]string
}

func (e *recordingEventEmitter) Emit(eventType string, data map[string]string) {
	e.types = append(e.types, eventType)
	e.payloads = append(e.payloads, data)
}

// A key created and deleted pushes two named events whose payloads carry
// the key's identifier and name and nothing else. The exact-map assertion
// is the point: the token the create response shows once can never ride
// the export surface.
func TestKeyLifecycleEmitsNamedEvents(t *testing.T) {
	handler, _ := newAdminTestController(t)
	emitter := &recordingEventEmitter{}
	handler.events = emitter

	router := chi.NewRouter()
	router.Post("/keys", handler.CreateKey)
	router.Delete("/keys/{key_id}", handler.DeleteKey)

	create := httptest.NewRequest(http.MethodPost, "/keys",
		bytes.NewReader([]byte(`{"name":"event-walk-key","scopes":["admin"]}`)))
	created := httptest.NewRecorder()
	router.ServeHTTP(created, create)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	var body struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &body))
	require.NotEmpty(t, body.Key.ID)

	del := httptest.NewRequest(http.MethodDelete, "/keys/"+body.Key.ID, nil)
	deleted := httptest.NewRecorder()
	router.ServeHTTP(deleted, del)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())

	require.Equal(t, []string{"key.created", "key.deleted"}, emitter.types)
	assert.Equal(t, map[string]string{
		"key_id": body.Key.ID, "name": "event-walk-key",
	}, emitter.payloads[0])
	assert.Equal(t, map[string]string{"key_id": body.Key.ID}, emitter.payloads[1])
}

// A mutation that fails pushes nothing: a receiver hears what happened,
// never what was merely attempted.
func TestAFailedMutationEmitsNothing(t *testing.T) {
	handler, _ := newAdminTestController(t)
	emitter := &recordingEventEmitter{}
	handler.events = emitter

	router := chi.NewRouter()
	router.Post("/keys", handler.CreateKey)

	create := httptest.NewRequest(http.MethodPost, "/keys",
		bytes.NewReader([]byte(`{not json`)))
	created := httptest.NewRecorder()
	router.ServeHTTP(created, create)
	require.Equal(t, http.StatusBadRequest, created.Code)

	assert.Empty(t, emitter.types)
}
