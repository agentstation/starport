package controllers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/blob"
	"github.com/stretchr/testify/require"
)

func TestPublicationReadinessHTTPRefusal(t *testing.T) {
	cause := errors.Join(blob.ErrPublicationUnavailable, errors.New("private bucket diagnostic"))
	t.Run("file", func(t *testing.T) {
		response := httptest.NewRecorder()
		(&FilesController{}).writeError(response, "upload", cause)
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		require.Contains(t, response.Body.String(), "conditional publication")
		require.NotContains(t, response.Body.String(), "private bucket")
	})
	t.Run("video", func(t *testing.T) {
		response := httptest.NewRecorder()
		NewVideosController(nil, nil).writeJobError(t.Context(), response, cause, "submit")
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		require.Contains(t, response.Body.String(), "conditional publication")
		require.NotContains(t, response.Body.String(), "private bucket")
	})
}
