package controllers

import (
	"encoding/json"
	"github.com/agentstation/starport/internal/policyrecord"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOversizedKeyPolicyReturnsActionableRefusal(t *testing.T) {
	handler, repository := newAdminTestController(t)
	body, err := json.Marshal(map[string]any{"name": "too-large", "scopes": []string{"chat:write"}, "metadata": map[string]string{"large": strings.Repeat("x", policyrecord.MaxBytes)}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.CreateKey(response, httptest.NewRequest(http.MethodPost, "/api/v1/admin/keys/", strings.NewReader(string(body))))
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "64 KiB") {
		t.Fatalf("refusal = %d %s", response.Code, response.Body.String())
	}
	records, err := repository.List(t.Context(), 10, 0)
	if err != nil || len(records) != 0 {
		t.Fatalf("refused key persisted: %d, %v", len(records), err)
	}
}
