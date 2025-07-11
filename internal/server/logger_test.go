package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestLoggingMiddleware(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	oldLogger := log.Logger
	log.Logger = zerolog.New(&buf)
	defer func() {
		log.Logger = oldLogger
	}()

	// Create a test handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	})

	// Wrap with logging middleware
	loggedHandler := LoggingMiddleware(handler)

	// Create test request
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	// Execute request
	loggedHandler.ServeHTTP(w, req)

	// Check that log was written
	logOutput := buf.String()
	if logOutput == "" {
		t.Error("expected log output, got empty string")
	}

	// Check log contains expected fields
	expectedFields := []string{
		"method",
		"path",
		"status",
		"duration",
		"request completed",
	}

	for _, field := range expectedFields {
		if !bytes.Contains(buf.Bytes(), []byte(field)) {
			t.Errorf("expected log to contain '%s'", field)
		}
	}
}

func TestLoggingMiddlewareWithError(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	oldLogger := log.Logger
	log.Logger = zerolog.New(&buf)
	defer func() {
		log.Logger = oldLogger
	}()

	// Create a test handler that returns an error
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error"))
	})

	// Wrap with logging middleware
	loggedHandler := LoggingMiddleware(handler)

	// Create test request
	req := httptest.NewRequest("POST", "/error", nil)
	w := httptest.NewRecorder()

	// Execute request
	loggedHandler.ServeHTTP(w, req)

	// Check that status 500 was logged
	logOutput := buf.String()
	if !bytes.Contains([]byte(logOutput), []byte("500")) {
		t.Error("expected log to contain status 500")
	}
}
