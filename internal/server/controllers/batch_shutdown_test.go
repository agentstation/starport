package controllers

import (
	"github.com/agentstation/starport/internal/jobs"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClosedBatchServiceReturnsRetryableStatus(t *testing.T) {
	controller := &BatchesController{}
	response := httptest.NewRecorder()
	controller.writeBatchError(t.Context(), response, jobs.ErrServiceClosed, "create batch")
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Contains(t, response.Body.String(), "shutting down")
}
