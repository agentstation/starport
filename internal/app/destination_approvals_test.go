package app

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApplicationRefusesUnapprovedCredentialDestination(t *testing.T) {
	fixture := newPerformanceFixtureWithApproval(t, 0, nil, false)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"openai/gpt-4o-mini","max_tokens":32,"messages":[{"role":"user","content":"Hello"}]}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := fixture.client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	select {
	case <-fixture.handlers:
	case <-time.After(time.Second):
		t.Fatal("destination refusal did not finish")
	}
	require.Zero(t, fixture.calls.Load(), "unapproved destination must receive no inference credential")
}

func TestApplicationUsesExplicitCredentialDestinationApproval(t *testing.T) {
	fixture := newPerformanceFixtureWithApproval(t, 0, nil, true)
	fixture.measure(t, true, false)
	fixture.measure(t, true, true)
	require.Equal(t, int64(2), fixture.calls.Load())
}
