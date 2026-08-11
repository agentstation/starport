package providers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/providerstate"
)

func TestProviderReconcilerDiscoversAmbientKey(t *testing.T) {
	provider := reconcilerTestProvider("ambient")
	cfg, err := config.NewLoader().
		WithPaths(config.PathsForConfigDir(t.TempDir())).
		WithEnvironment(map[string]string{"OPENAI_API_KEY": "test-ambient"}).
		WithEnvFiles().
		Load(t.Context())
	require.NoError(t, err)
	reconciler, published := newTestReconciler(t, []catalogs.Provider{provider}, cfg)
	report, err := reconciler.Reconcile(t.Context(), false)
	require.NoError(t, err)
	require.True(t, report.Changed)
	require.Equal(t, []catalogs.ProviderID{provider.ID}, report.ConfiguredProviders)
	require.Equal(t, int32(1), published.Load())
}

func TestProviderReconcilerManualRefreshSharesInflight(t *testing.T) {
	provider := reconcilerTestProvider("manual")
	entered := make(chan struct{})
	release := make(chan struct{})
	resolver := &reconcilerTestResolver{resolve: func(ctx context.Context, _ catalogs.Provider) (config.ProviderConfig, bool, error) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		select {
		case <-release:
			return reconcilerProviderConfig(provider), true, nil
		case <-ctx.Done():
			return config.ProviderConfig{}, false, ctx.Err()
		}
	}}
	reconciler, published := newTestReconciler(t, []catalogs.Provider{provider}, resolver)
	require.NoError(t, reconciler.Adopt(reconcilerTestView(provider), nil))

	results := make(chan error, 2)
	go func() {
		_, err := reconciler.Reconcile(t.Context(), true)
		results <- err
	}()
	<-entered
	go func() {
		_, err := reconciler.Reconcile(t.Context(), true)
		results <- err
	}()
	require.Eventually(t, func() bool {
		reconciler.flightMu.Lock()
		defer reconciler.flightMu.Unlock()
		return reconciler.inflight != nil && reconciler.inflight.waiters == 1
	}, time.Second, time.Millisecond)
	close(release)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.Equal(t, int32(1), resolver.calls.Load())
	require.Equal(t, int32(1), published.Load())
}

func TestProviderFailureDoesNotBlockOthers(t *testing.T) {
	failing := reconcilerTestProvider("failing")
	working := reconcilerTestProvider("working")
	resolver := &reconcilerTestResolver{resolve: func(_ context.Context, provider catalogs.Provider) (config.ProviderConfig, bool, error) {
		if provider.ID == failing.ID {
			return config.ProviderConfig{}, false, errors.New("source unavailable")
		}
		return reconcilerProviderConfig(provider), true, nil
	}}
	reconciler, published := newTestReconciler(t, []catalogs.Provider{failing, working}, resolver)
	require.NoError(t, reconciler.Adopt(reconcilerTestView(failing, working), nil))

	report, err := reconciler.Reconcile(t.Context(), true)
	require.NoError(t, err)
	require.Equal(t, []catalogs.ProviderID{working.ID}, report.ConfiguredProviders)
	require.Len(t, report.Failures, 1)
	require.Equal(t, failing.ID, report.Failures[0].ProviderID)
	require.Equal(t, int32(1), published.Load())
}

func TestProviderReconcilerCancellationStops(t *testing.T) {
	provider := reconcilerTestProvider("cancel")
	resolver := &reconcilerTestResolver{resolve: func(ctx context.Context, _ catalogs.Provider) (config.ProviderConfig, bool, error) {
		<-ctx.Done()
		return config.ProviderConfig{}, false, ctx.Err()
	}}
	reconciler, published := newTestReconciler(t, []catalogs.Provider{provider}, resolver)
	require.NoError(t, reconciler.Adopt(reconcilerTestView(provider), nil))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := reconciler.Reconcile(ctx, true)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, int32(0), published.Load())
}

func TestProviderReconcilerPublishesCredentialLifecycle(t *testing.T) {
	provider := reconcilerTestProvider("lifecycle")
	states := &credentialStateCapture{}
	resolver := &reconcilerTestResolver{resolve: func(
		context.Context,
		catalogs.Provider,
	) (config.ProviderConfig, bool, error) {
		return reconcilerProviderConfig(provider), true, nil
	}}
	view := reconcilerTestView(provider)
	reconciler, err := NewReconciler(
		func() (CatalogView, error) { return view, nil },
		resolver,
		nil,
		func(context.Context, CatalogView, config.ProvidersConfig) error { return nil },
		time.Second,
		states,
	)
	require.NoError(t, err)
	_, err = reconciler.Reconcile(t.Context(), false)
	require.NoError(t, err)
	generations := states.snapshot()
	require.GreaterOrEqual(t, len(generations), 2)
	require.Equal(t, providerstate.CredentialRefreshing, generations[0].Observations[0].State)
	last := generations[len(generations)-1].Observations[0]
	require.Equal(t, providerstate.CredentialReady, last.State)
	require.True(t, last.Usable)
	require.Equal(t, "test-version", last.MaterialVersion)

	deniedStates := &credentialStateCapture{}
	deniedResolver := &reconcilerTestResolver{resolve: func(
		context.Context,
		catalogs.Provider,
	) (config.ProviderConfig, bool, error) {
		return config.ProviderConfig{}, false, credentials.NewSourceError(
			credentials.SourceErrorDenied,
			credentials.ReferenceBackendEnvironment,
		)
	}}
	denied, err := NewReconciler(
		func() (CatalogView, error) { return view, nil },
		deniedResolver,
		nil,
		func(context.Context, CatalogView, config.ProvidersConfig) error { return nil },
		time.Second,
		deniedStates,
	)
	require.NoError(t, err)
	_, err = denied.Reconcile(t.Context(), true)
	require.NoError(t, err)
	deniedGenerations := deniedStates.snapshot()
	deniedLast := deniedGenerations[len(deniedGenerations)-1].Observations[0]
	require.Equal(t, providerstate.CredentialDenied, deniedLast.State)
	require.Equal(t, providerstate.ReasonOperatorSourceDenied, deniedLast.Reason)
}

type reconcilerTestResolver struct {
	calls   atomic.Int32
	resolve func(context.Context, catalogs.Provider) (config.ProviderConfig, bool, error)
}

type credentialStateCapture struct {
	mu          sync.Mutex
	generations []providerstate.CredentialGeneration
}

func (c *credentialStateCapture) PublishCredentials(generation providerstate.CredentialGeneration) {
	c.mu.Lock()
	generation.Observations = append(
		[]providerstate.CredentialObservation(nil),
		generation.Observations...,
	)
	c.generations = append(c.generations, generation)
	c.mu.Unlock()
}

func (c *credentialStateCapture) snapshot() []providerstate.CredentialGeneration {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]providerstate.CredentialGeneration, len(c.generations))
	copy(result, c.generations)
	return result
}

func (*reconcilerTestResolver) ValidateProviderCredentialContracts([]catalogs.Provider) error {
	return nil
}

func (r *reconcilerTestResolver) ResolveProviderRuntime(
	ctx context.Context,
	provider catalogs.Provider,
	_ config.ProviderConfig,
	_ bool,
) (config.ProviderConfig, bool, error) {
	r.calls.Add(1)
	if r.resolve != nil {
		return r.resolve(ctx, provider)
	}
	return config.ProviderConfig{}, false, nil
}

func newTestReconciler(
	t *testing.T,
	providers []catalogs.Provider,
	resolver CredentialRuntimeResolver,
) (*Reconciler, *atomic.Int32) {
	t.Helper()
	view := reconcilerTestView(providers...)
	published := &atomic.Int32{}
	reconciler, err := NewReconciler(
		func() (CatalogView, error) { return view, nil },
		resolver,
		nil,
		func(context.Context, CatalogView, config.ProvidersConfig) error {
			published.Add(1)
			return nil
		},
		time.Second,
		nil,
	)
	require.NoError(t, err)
	return reconciler, published
}

func reconcilerTestView(providers ...catalogs.Provider) CatalogView {
	return CatalogView{
		GenerationID: "test-generation", PayloadChecksum: "test-checksum",
		Providers: providers,
	}
}

func reconcilerTestProvider(providerID catalogs.ProviderID) catalogs.Provider {
	return catalogs.Provider{
		ID: providerID, Name: string(providerID),
		Credentials: &catalogs.ProviderCredentials{
			Fields: []catalogs.ProviderCredentialField{{
				ID: "api-key", Kind: catalogs.ProviderCredentialFieldSecret, Required: true,
				Environment: []string{"OPENAI_API_KEY"}, Pattern: `^test-`,
			}},
			Profiles: []catalogs.ProviderCredentialProfile{{
				ID: "api-key", Primitive: catalogs.ProviderAuthenticationAPIKey,
				Fields: []catalogs.ProviderCredentialFieldID{"api-key"},
				Placements: []catalogs.ProviderCredentialPlacement{{
					Field: "api-key", Kind: catalogs.ProviderCredentialPlacementHeader,
					Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
				}},
			}},
			Inference: catalogs.ProviderCredentialPlane{
				Required: true, Alternatives: []catalogs.ProviderCredentialProfileID{"api-key"},
			},
		},
	}
}

func reconcilerProviderConfig(provider catalogs.Provider) config.ProviderConfig {
	profile := provider.Credentials.Profiles[0]
	material := credentials.NewMaterial(
		profile,
		map[catalogs.ProviderCredentialFieldID]string{"api-key": "test-key"},
		credentials.MaterialMetadata{Version: "test-version"},
	)
	return config.ProviderConfig{
		Material: material, CredentialSource: staticReconcilerMaterialSource{material: material},
		Timeout: time.Second, MaxConnections: 1, Enabled: true,
	}
}

type staticReconcilerMaterialSource struct{ material credentials.Material }

func (s staticReconcilerMaterialSource) ResolveMaterial(context.Context) (credentials.Material, error) {
	return s.material, nil
}
