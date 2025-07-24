package chatui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

func TestNewHandler(t *testing.T) {
	logger := zerolog.Nop()
	config := Config{
		Title:       "Test Chat",
		Theme:       "light",
		AllowKeyGen: true,
		APIBaseURL:  "http://localhost:8080",
	}

	handler, err := NewHandler(&logger, config)
	require.NoError(t, err)
	assert.NotNil(t, handler)
	assert.Equal(t, config, handler.config)
}

func TestHandler_Index(t *testing.T) {
	logger := zerolog.Nop()
	config := Config{
		Title:       "Test Chat",
		Theme:       "dark",
		AllowKeyGen: true,
		APIBaseURL:  "http://localhost:8080",
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
		Title:       "Test Chat",
		Theme:       "light",
		AllowKeyGen: false,
		APIBaseURL:  "http://localhost:8080",
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

func TestHandler_GenerateKey(t *testing.T) {
	logger := zerolog.Nop()

	t.Run("key generation disabled", func(t *testing.T) {
		config := Config{
			Title:       "Test Chat",
			Theme:       "light",
			AllowKeyGen: false,
			APIBaseURL:  "http://localhost:8080",
		}

		handler, err := NewHandler(&logger, config)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/generate-key", nil)
		rec := httptest.NewRecorder()

		handler.GenerateKey(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Contains(t, rec.Body.String(), "Key generation is disabled")
	})

	t.Run("storage not configured", func(t *testing.T) {
		config := Config{
			Title:       "Test Chat",
			Theme:       "light",
			AllowKeyGen: true,
			APIBaseURL:  "http://localhost:8080",
			Store:       nil,
		}

		handler, err := NewHandler(&logger, config)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/generate-key", nil)
		rec := httptest.NewRecorder()

		handler.GenerateKey(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "Storage not configured")
	})

	t.Run("successful key generation", func(t *testing.T) {
		store := storage.NewMockStore()
		config := Config{
			Title:       "Test Chat",
			Theme:       "light",
			AllowKeyGen: true,
			APIBaseURL:  "http://localhost:8080",
			Store:       store,
		}

		// Use a real logger to see errors
		testLogger := zerolog.New(zerolog.NewTestWriter(t))
		handler, err := NewHandler(&testLogger, config)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/generate-key", nil)
		rec := httptest.NewRecorder()

		handler.GenerateKey(rec, req)

		if rec.Code != http.StatusOK {
			t.Logf("Response body: %s", rec.Body.String())
		}

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		var response map[string]interface{}
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Contains(t, response, "key")
		assert.Contains(t, response, "key_id")
		assert.Contains(t, response, "message")
		assert.Contains(t, response, "scopes")

		// Check that key starts with expected prefix
		key, ok := response["key"].(string)
		assert.True(t, ok)
		assert.True(t, len(key) > 20)
		assert.Contains(t, key, "STARPORT_")

		// Check scopes
		scopes, ok := response["scopes"].([]interface{})
		assert.True(t, ok)
		assert.Contains(t, scopes, "chat:write")
		assert.Contains(t, scopes, "models:read")
	})
}

func TestHandler_Routes(t *testing.T) {
	logger := zerolog.Nop()

	t.Run("routes without key generation", func(t *testing.T) {
		config := Config{
			Title:       "Test Chat",
			Theme:       "light",
			AllowKeyGen: false,
			APIBaseURL:  "http://localhost:8080",
		}

		handler, err := NewHandler(&logger, config)
		require.NoError(t, err)

		router := handler.Routes()
		assert.NotNil(t, router)

		// Test that generate-key route is not available
		req := httptest.NewRequest(http.MethodPost, "/generate-key", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("routes with key generation", func(t *testing.T) {
		config := Config{
			Title:       "Test Chat",
			Theme:       "light",
			AllowKeyGen: true,
			APIBaseURL:  "http://localhost:8080",
		}

		handler, err := NewHandler(&logger, config)
		require.NoError(t, err)

		router := handler.Routes()
		assert.NotNil(t, router)

		// Test that generate-key route is available
		req := httptest.NewRequest(http.MethodPost, "/generate-key", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		// Should get forbidden because storage is not configured, not 404
		assert.NotEqual(t, http.StatusNotFound, rec.Code)
	})
}