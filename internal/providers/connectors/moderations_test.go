package connectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

// TestTheModerationTransportSpeaksTheOpenAIWire holds the transport half of
// ENR-V15. One canonical request produces the published body, and the wire's
// two per-category maps reduce to one sorted verdict list, so nothing above
// the transport parses provider JSON.
func TestTheModerationTransportSpeaksTheOpenAIWire(t *testing.T) {
	t.Parallel()

	var sent map[string]any
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &sent))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "id": "modr-1",
		  "model": "omni-moderation-2024-09-26",
		  "results": [{
		    "flagged": true,
		    "categories": {"violence": true, "harassment": false},
		    "category_scores": {"harassment": 0.02, "violence": 0.94}
		  }]
		}`))
	}))
	defer server.Close()

	moderator := productionModerator(t, server.URL)
	response, err := moderator.Moderate(context.Background(), &ModerationRequest{
		MediaTarget: MediaTarget{
			Model:      "omni-moderation-latest",
			Endpoint:   InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL},
			Credential: testAPIMaterial("moderation-key"),
		},
		Inputs: []string{"I want to hurt someone."},
	})
	require.NoError(t, err)

	require.Equal(t, "omni-moderation-latest", sent["model"])
	require.Equal(t, []any{"I want to hurt someone."}, sent["input"])
	// The credential is placed through the provider auth registry rather than
	// written by the transport.
	require.Equal(t, "Bearer moderation-key", authorization)

	require.Equal(t, "modr-1", response.ID)
	require.Equal(t, "omni-moderation-2024-09-26", response.Model)
	require.Equal(t, []ModerationResult{{
		Flagged: true,
		Categories: []ModerationCategory{
			{Name: "harassment", Flagged: false, Score: 0.02},
			{Name: "violence", Flagged: true, Score: 0.94},
		},
	}}, response.Results)
}

// TestACategoryInOneWireMapStillAnswers guards the join. A score with no
// threshold decision and a decision with no score are both verdicts the
// caller should read, so a name in either map lands in the verdict list.
func TestACategoryInOneWireMapStillAnswers(t *testing.T) {
	t.Parallel()

	categories := joinModerationCategories(
		map[string]bool{"illicit": true},
		map[string]float64{"violence": 0.4},
	)
	require.Equal(t, []ModerationCategory{
		{Name: "illicit", Flagged: true, Score: 0},
		{Name: "violence", Flagged: false, Score: 0.4},
	}, categories)
}

// TestAModerationAnswerWithTheWrongResultCountIsRefused stops a provider
// defect from becoming a wrong answer. A verdict answers the input at the
// same position, so a shorter list silently answers the wrong input.
func TestAModerationAnswerWithTheWrongResultCountIsRefused(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"modr-1","model":"m","results":[]}`))
	}))
	defer server.Close()

	moderator := productionModerator(t, server.URL)
	_, err := moderator.Moderate(context.Background(), &ModerationRequest{
		MediaTarget: MediaTarget{
			Model:      "omni-moderation-latest",
			Endpoint:   InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL},
			Credential: testAPIMaterial("moderation-key"),
		},
		Inputs: []string{"one", "two"},
	})
	require.ErrorContains(t, err, "0 moderation results for 2 inputs")
}

// TestAnEmptyModerationRequestNeverReachesTheProvider keeps the paid error at
// home. The canonical constructor refuses the same request, and the transport
// holds its own guard because a transport is also reachable through code that
// did not use the constructor.
func TestAnEmptyModerationRequestNeverReachesTheProvider(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a request with nothing to classify reached the provider")
	}))
	defer server.Close()

	moderator := productionModerator(t, server.URL)
	_, err := moderator.Moderate(context.Background(), &ModerationRequest{
		MediaTarget: MediaTarget{
			Model:      "omni-moderation-latest",
			Endpoint:   InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL},
			Credential: testAPIMaterial("moderation-key"),
		},
	})
	require.ErrorIs(t, err, ErrInvalidMediaRequest)
}

// TestAConnectorWithoutTheModerationOperationRefuses is what a router reads
// before it spends a credential. The OpenAI transport serves moderation and
// the rerank transports serve none of it, and the probe reads the transport
// the route selected rather than the composed connector.
func TestAConnectorWithoutTheModerationOperationRefuses(t *testing.T) {
	t.Parallel()

	registry, err := ProductionTransportRegistry()
	require.NoError(t, err)
	connector, err := registry.NewProviderConnector(
		"acme",
		[]catalogs.EndpointType{catalogs.EndpointTypeOpenAI, catalogs.EndpointTypeCohere},
		mediaTestConfig("https://provider.example"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connector.Close() })

	_, openAIServes := ModeratorFor(connector, catalogs.EndpointTypeOpenAI)
	require.True(t, openAIServes)
	_, cohereServes := ModeratorFor(connector, catalogs.EndpointTypeCohere)
	require.False(t, cohereServes)

	// Moderation stays off Connector for the reason Reranker stands apart: one
	// compiled transport serves it, and a method every transport answered would
	// stop the compiler reporting which ones actually can.
	require.Equal(t, 1, reflect.TypeOf((*Moderator)(nil)).Elem().NumMethod())
}

// productionModerator composes one provider through the shipped registry, so a
// test exercises the descriptor the gateway actually registers rather than a
// transport built beside it.
func productionModerator(t *testing.T, baseURL string) Moderator {
	t.Helper()
	registry, err := ProductionTransportRegistry()
	require.NoError(t, err)
	connector, err := registry.NewProviderConnector(
		"openai",
		[]catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
		mediaTestConfig(baseURL),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connector.Close() })
	moderator, implemented := ModeratorFor(connector, catalogs.EndpointTypeOpenAI)
	require.True(t, implemented)
	return moderator
}
