package router

import (
	"context"
	"sync"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
)

// scopedCredentialStore serves stored credentials by scope and remembers which
// scopes were asked, in order. The order is the contract under test: a
// strategy decides which plane a request reaches and which it never touches.
type scopedCredentialStore struct {
	mu        sync.Mutex
	byScope   map[string]credentials.Material
	consulted []string
}

func newScopedCredentialStore(byScope map[string]credentials.Material) *scopedCredentialStore {
	return &scopedCredentialStore{byScope: byScope}
}

func (s *scopedCredentialStore) ResolveStoredMaterial(
	_ context.Context,
	scope string,
	_ catalogs.Provider,
) (credentials.Material, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consulted = append(s.consulted, scope)
	material, exists := s.byScope[scope]
	if !exists {
		return credentials.Material{}, keyring.ErrKeyNotFound
	}
	return material, nil
}

func (s *scopedCredentialStore) scopes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.consulted...)
}

// gatewayCredentialFixture builds a deployment whose environment holds no
// credential for the provider, so every test here observes which stored plane
// the strategy actually reaches.
type gatewayCredentialFixture struct {
	router   *modelRouter
	runtime  *embeddingTestRuntime
	store    *scopedCredentialStore
	accepted []string
}

func newGatewayCredentialFixture(
	t *testing.T,
	byScope map[string]credentials.Material,
) *gatewayCredentialFixture {
	t.Helper()
	plane := embeddingTestCatalogPlane(t)
	fixture := &gatewayCredentialFixture{store: newScopedCredentialStore(byScope)}
	connector := &mockConnector{name: "acme"}
	connector.embeddingsFunc = func(_ context.Context, req *connectors.EmbeddingsRequest) (*connectors.EmbeddingsResponse, error) {
		fixture.accepted = append(fixture.accepted, req.Credential.Version())
		return &connectors.EmbeddingsResponse{
			Object: "list", Model: req.Model,
			Data: []connectors.Embedding{{Object: "embedding", Index: 0, Embedding: []float32{1}}},
		}, nil
	}
	fixture.runtime = &embeddingTestRuntime{
		snapshot:  plane.Current(),
		connector: connector,
		operatorErr: credentials.NewSourceError(
			credentials.SourceErrorNotConfigured, "no environment credential",
		),
	}
	fixture.router = New(
		&embeddingTestRegistry{runtime: fixture.runtime},
		WithCatalog(plane),
		WithStoredCredentials(fixture.store),
	).(*modelRouter)
	return fixture
}

func (f *gatewayCredentialFixture) route(
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

// TestGatewayCredentialServesARequestWithNoBYOK is the AON3 fail-before case.
// On the baseline the policy knew two planes, environment and account, so a
// credential the operator applied for the whole deployment had no consumer at
// all and this request failed as not configured.
func TestGatewayCredentialServesARequestWithNoBYOK(t *testing.T) {
	fixture := newGatewayCredentialFixture(t, map[string]credentials.Material{
		keyring.GatewayScope: embeddingTestMaterial("gateway"),
	})

	response, err := fixture.route(t, keyring.OperatorFirst)
	require.NoError(t, err)
	require.Equal(t, "acme/opaque/embed@002", response.ModelUsed)
	assert.Equal(t, []string{"gateway"}, fixture.accepted,
		"the request must be served by the operator's gateway credential")
	assert.Equal(t, []string{keyring.GatewayScope}, fixture.store.scopes(),
		"operator_first reaches the gateway plane before the account's own")
	assert.Equal(t, int64(1), fixture.runtime.operatorCalls.Load(),
		"the environment plane is still tried first")
}

// TestBYOKWinsOverAGatewayCredentialUnderBYOKFirst proves the order is real
// and not an artifact of one plane being empty.
func TestBYOKWinsOverAGatewayCredentialUnderBYOKFirst(t *testing.T) {
	fixture := newGatewayCredentialFixture(t, map[string]credentials.Material{
		keyring.GatewayScope:               embeddingTestMaterial("gateway"),
		keyring.AccountScope("account-a"):  embeddingTestMaterial("byok"),
		keyring.AccountScope("other-acct"): embeddingTestMaterial("other-byok"),
	})

	_, err := fixture.route(t, keyring.BYOKFirst)
	require.NoError(t, err)
	assert.Equal(t, []string{"byok"}, fixture.accepted)
	assert.Equal(t, []string{keyring.AccountScope("account-a")}, fixture.store.scopes(),
		"a served BYOK credential must not cause the gateway plane to be read")
	assert.Zero(t, fixture.runtime.operatorCalls.Load(),
		"byok_first must not probe the environment when the account's own credential serves")
}

// TestBYOKOnlyNeverReachesAGatewayCredential is the deny story. An operator
// who sets an account to byok_only is withholding the deployment's money, and
// a gateway credential is exactly that money.
func TestBYOKOnlyNeverReachesAGatewayCredential(t *testing.T) {
	fixture := newGatewayCredentialFixture(t, map[string]credentials.Material{
		keyring.GatewayScope: embeddingTestMaterial("gateway"),
	})

	_, err := fixture.route(t, keyring.BYOKOnly)
	require.Error(t, err)
	var providerFailure *failure.Failure
	require.ErrorAs(t, err, &providerFailure)
	assert.Equal(t, "Provider credentials are not configured.", providerFailure.SafeMessage())
	assert.Empty(t, fixture.accepted, "no attempt may be paid for by the operator")
	assert.Equal(t, []string{keyring.AccountScope("account-a")}, fixture.store.scopes(),
		"byok_only must never read the gateway scope")
	assert.Zero(t, fixture.runtime.operatorCalls.Load())
}

// TestNoCredentialInAnySourceReportsNotConfigured pins the exhausted case: all
// three planes are consulted in order and the caller still gets the one
// external not-configured shape rather than a panic or an internal error.
func TestNoCredentialInAnySourceReportsNotConfigured(t *testing.T) {
	fixture := newGatewayCredentialFixture(t, nil)

	_, err := fixture.route(t, keyring.OperatorFirst)
	require.Error(t, err)
	var providerFailure *failure.Failure
	require.ErrorAs(t, err, &providerFailure)
	assert.Equal(t, failure.Authentication, providerFailure.Kind())
	assert.Equal(t, "Provider credentials are not configured.", providerFailure.SafeMessage())
	assert.Equal(t, int64(1), fixture.runtime.operatorCalls.Load())
	assert.Equal(t,
		[]string{keyring.GatewayScope, keyring.AccountScope("account-a")},
		fixture.store.scopes(),
		"operator_first orders environment, then gateway, then BYOK")
}

// TestGatewayCredentialIsAttributedToTheOperator guards usage attribution. A
// gateway credential is the operator's money even though it is stored rather
// than read from the environment, so recording it as account-owned would bill
// the wrong party for every request an operator paid for.
func TestGatewayCredentialIsAttributedToTheOperator(t *testing.T) {
	material := embeddingTestMaterial("v1")
	for source, want := range map[keyring.CredentialSource]execution.CredentialOwner{
		keyring.SourceEnvironment: execution.CredentialOwnerOperator,
		keyring.SourceGateway:     execution.CredentialOwnerOperator,
		keyring.SourceBYOK:        execution.CredentialOwnerAccount,
	} {
		t.Run(string(source), func(t *testing.T) {
			assert.Equal(t, want, credentialEvidence(source, material).Owner)
		})
	}
}

// TestUnreachableStoredPlanesNeverWidenAStrategy covers the deployment that
// has no credential store at all. Dropping the planes it cannot read must not
// hand a byok_only account an operator credential by accident.
func TestUnreachableStoredPlanesNeverWidenAStrategy(t *testing.T) {
	tests := []struct {
		name      string
		strategy  keyring.Strategy
		hasStore  bool
		accountID string
		want      []keyring.CredentialSource
	}{
		{
			name: "no store keeps only the environment", strategy: keyring.OperatorFirst,
			hasStore: false, accountID: "account-a",
			want: []keyring.CredentialSource{keyring.SourceEnvironment},
		},
		{
			name: "no store leaves byok_only with nothing", strategy: keyring.BYOKOnly,
			hasStore: false, accountID: "account-a",
			want: []keyring.CredentialSource{},
		},
		{
			name: "no account drops only the BYOK plane", strategy: keyring.OperatorFirst,
			hasStore: true, accountID: "",
			want: []keyring.CredentialSource{
				keyring.SourceEnvironment, keyring.SourceGateway,
			},
		},
		{
			name: "no account leaves byok_only with nothing", strategy: keyring.BYOKOnly,
			hasStore: true, accountID: "",
			want: []keyring.CredentialSource{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var store StoredCredentialResolver
			if test.hasStore {
				store = newScopedCredentialStore(nil)
			}
			assert.Equal(t, test.want, reachableSources(test.strategy, test.accountID, store))
		})
	}
}
