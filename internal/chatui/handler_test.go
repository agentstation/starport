package chatui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHandler(t *testing.T) {
	logger := zerolog.Nop()
	config := Config{
		Title:      "Test Chat",
		Theme:      "light",
		APIBaseURL: "http://localhost:8080",
	}

	handler, err := NewHandler(&logger, config)
	require.NoError(t, err)
	assert.NotNil(t, handler)
	assert.Equal(t, config, handler.config)
}

func TestChatUIUsesCanonicalStarmapProviderIDs(t *testing.T) {
	for _, providerID := range []string{"google-ai-studio", "google-vertex", "azure-openai"} {
		require.Contains(t, chatJS, providerID)
	}
	for _, legacy := range []string{"case 'google':", "vertexai:", `model: "google/`} {
		require.NotContains(t, chatJS, legacy)
	}
}

func TestHandler_Index(t *testing.T) {
	logger := zerolog.Nop()
	config := Config{
		Title:      "Test Chat",
		Theme:      "dark",
		APIBaseURL: "http://localhost:8080",
	}

	handler, err := NewHandler(&logger, config)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.Index(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))

	// Check that the template was rendered with correct values
	body := rec.Body.String()
	assert.Contains(t, body, "Test Chat")
	assert.Contains(t, body, `data-theme="dark"`)
	assert.Contains(t, body, "http://localhost:8080")
}

func TestHandler_Static(t *testing.T) {
	logger := zerolog.Nop()
	config := Config{
		Title:      "Test Chat",
		Theme:      "light",
		APIBaseURL: "http://localhost:8080",
	}

	handler, err := NewHandler(&logger, config)
	require.NoError(t, err)

	tests := []struct {
		name        string
		path        string
		wantStatus  int
		wantContent string
		wantType    string
	}{
		{
			name:        "chat.js",
			path:        "chat.js",
			wantStatus:  http.StatusOK,
			wantContent: "Starport ChatUI JavaScript Client",
			wantType:    "application/javascript",
		},
		{
			name:        "chat.css",
			path:        "chat.css",
			wantStatus:  http.StatusOK,
			wantContent: "Reset and Base Styles",
			wantType:    "text/css",
		},
		{
			name:       "unknown file",
			path:       "unknown.txt",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/static/"+tt.path, nil)
			rec := httptest.NewRecorder()

			// Set URL param for chi router
			ctx := context.WithValue(req.Context(), chi.RouteCtxKey, &chi.Context{
				URLParams: chi.RouteParams{
					Keys:   []string{"*"},
					Values: []string{tt.path},
				},
			})
			req = req.WithContext(ctx)

			handler.Static(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantStatus == http.StatusOK {
				assert.Contains(t, rec.Body.String(), tt.wantContent)
				assert.Equal(t, tt.wantType, rec.Header().Get("Content-Type"))
				assert.Equal(t, "public, max-age=3600", rec.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestHandler_Routes(t *testing.T) {
	logger := zerolog.Nop()
	config := Config{
		Title:      "Test Chat",
		Theme:      "light",
		APIBaseURL: "http://localhost:8080",
	}

	handler, err := NewHandler(&logger, config)
	require.NoError(t, err)

	router := handler.Routes()
	assert.NotNil(t, router)

	req := httptest.NewRequest(http.MethodPost, "/generate-key", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
