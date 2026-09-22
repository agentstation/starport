package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/failure"
	"github.com/stretchr/testify/require"
)

func TestCatalogPermissionFailureUsesRetryableGatewayStatus(t *testing.T) {
	refusal := failure.New(failure.Kind("gateway_unavailable"), "Catalog permission is unavailable.", true, failure.ProviderDetails{}, nil)
	for _, protocol := range []Protocol{ProtocolOpenAI, ProtocolOpenRouter} {
		t.Run(string(protocol), func(t *testing.T) {
			handler := &BaseHandler{protocol: protocol}
			response := httptest.NewRecorder()
			handler.writeError(response, refusal)
			require.Equal(t, http.StatusServiceUnavailable, response.Code)
			require.Contains(t, response.Body.String(), "Catalog permission is unavailable.")
		})
	}
}
