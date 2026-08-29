package router

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
)

// credentialSourceFixture builds a deployment whose credential planes are
// filled one at a time, so the plane that paid for a request is unambiguous.
// The provider records every credential version it was offered and can be told
// to reject one, which is how the fallback case gets two planes into a single
// request.
type credentialSourceFixture struct {
	router   *modelRouter
	runtime  *embeddingTestRuntime
	mu       sync.Mutex
	offered  []string
	rejected map[string]bool
}

func newCredentialSourceFixture(
	t *testing.T,
	environmentVersion string,
	stored map[string]credentials.Material,
	rejected ...string,
) *credentialSourceFixture {
	t.Helper()
	plane := embeddingTestCatalogPlane(t)
	fixture := &credentialSourceFixture{rejected: make(map[string]bool, len(rejected))}
	for _, version := range rejected {
		fixture.rejected[version] = true
	}
	connector := &mockConnector{name: "acme"}
	connector.embeddingsFunc = func(_ context.Context, req *connectors.EmbeddingsRequest) (*connectors.EmbeddingsResponse, error) {
		version := req.Credential.Version()
		fixture.mu.Lock()
		fixture.offered = append(fixture.offered, version)
		reject := fixture.rejected[version]
		fixture.mu.Unlock()
		if reject {
			return nil, connectors.ErrInvalidAPIKey
		}
		return &connectors.EmbeddingsResponse{
			Object: "list", Model: req.Model,
			Data: []connectors.Embedding{{Object: "embedding", Index: 0, Embedding: []float32{1}}},
		}, nil
	}
	fixture.runtime = &embeddingTestRuntime{snapshot: plane.Current(), connector: connector}
	if environmentVersion == "" {
		fixture.runtime.operatorErr = credentials.NewSourceError(
			credentials.SourceErrorNotConfigured, "no environment credential",
		)
	} else {
		fixture.runtime.operator = embeddingTestMaterial(environmentVersion)
	}
	fixture.router = New(
		&embeddingTestRegistry{runtime: fixture.runtime},
		WithCatalog(plane),
		WithStoredCredentials(newScopedCredentialStore(stored)),
	).(*modelRouter)
	return fixture
}

func (f *credentialSourceFixture) route(
	t *testing.T,
	strategy keyring.Strategy,
) (*EmbeddingResponse, error) {
	t.Helper()
	return f.router.RouteEmbeddings(t.Context(), &EmbeddingRequest{
		EmbeddingsRequest: &connectors.EmbeddingsRequest{Model: "author/embed", Input: "hello"},
		AccountID:         "account-a",
		APIKeyConfig:      &APIKeyConfig{CredentialStrategy: strategy},
	})
}

func (f *credentialSourceFixture) offeredVersions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.offered...)
}

// TestServedCredentialSourceNamesThePlaneThatPaid drives one request per plane.
// Without it an operator reading the activity log cannot tell an account
// spending its own BYOK from one spending the deployment's credential, which is
// the difference between a bill they expected and one they did not.
func TestServedCredentialSourceNamesThePlaneThatPaid(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		stored      map[string]credentials.Material
		strategy    keyring.Strategy
		want        keyring.CredentialSource
	}{
		{
			name: "environment", environment: "env",
			strategy: keyring.OperatorFirst, want: keyring.SourceEnvironment,
		},
		{
			name: "shared",
			stored: map[string]credentials.Material{
				keyring.SharedScope: embeddingTestMaterial("gw"),
			},
			strategy: keyring.OperatorFirst, want: keyring.SourceShared,
		},
		{
			name: "byok",
			stored: map[string]credentials.Material{
				keyring.AccountScope("account-a"): embeddingTestMaterial("byok"),
			},
			strategy: keyring.BYOKFirst, want: keyring.SourceBYOK,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCredentialSourceFixture(t, test.environment, test.stored)

			response, err := fixture.route(t, test.strategy)
			require.NoError(t, err)
			assert.Equal(t, string(test.want), response.CredentialSource)
		})
	}
}

// TestServedCredentialSourceFollowsTheFallback pins the case a per-request
// field is easy to get wrong. The environment plane is tried first and the
// provider refuses its credential, so the shared plane is what actually paid.
// Recording the first plane the policy reached would report the deployment
// running on a shell variable that in fact served nothing.
func TestServedCredentialSourceFollowsTheFallback(t *testing.T) {
	fixture := newCredentialSourceFixture(t, "env",
		map[string]credentials.Material{
			keyring.SharedScope: embeddingTestMaterial("gw"),
		},
		"env",
	)

	response, err := fixture.route(t, keyring.OperatorFirst)
	require.NoError(t, err)
	assert.Equal(t, []string{"env", "gw"}, fixture.offeredVersions(),
		"the environment credential must be offered first and refused")
	assert.Equal(t, string(keyring.SourceShared), response.CredentialSource)
}
