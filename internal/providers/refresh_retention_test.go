package providers

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	providerstate "github.com/agentstation/starport/internal/providers/state"
	"github.com/stretchr/testify/require"
)

func TestReconcilerTerminalFailureRemovesPriorProvider(t *testing.T) {
	for _, kind := range []credentials.SourceErrorKind{credentials.SourceErrorDenied, credentials.SourceErrorInvalid, credentials.SourceErrorNotConfigured, credentials.SourceErrorUnavailable} {
		t.Run(string(kind), func(t *testing.T) {
			provider := reconcilerTestProvider("provider")
			failed := false
			resolver := &reconcilerTestResolver{resolve: func(context.Context, catalogs.Provider) (config.ProviderConfig, bool, error) {
				if failed {
					return config.ProviderConfig{}, false, credentials.NewSourceError(kind, "test")
				}
				return reconcilerProviderConfig(provider), true, nil
			}}
			states := &credentialStateCapture{}
			view := reconcilerTestView(provider)
			var published config.ProvidersConfig
			reconciler, err := NewReconciler(func() (CatalogView, error) { return view, nil }, resolver, nil, func(_ context.Context, _ CatalogView, next config.ProvidersConfig) error {
				published = next
				return nil
			}, time.Second, states)
			require.NoError(t, err)
			_, err = reconciler.Reconcile(t.Context(), false)
			require.NoError(t, err)
			require.Contains(t, published, provider.ID)
			failed = true
			_, err = reconciler.Reconcile(t.Context(), true)
			require.NoError(t, err)
			generations := states.snapshot()
			last := generations[len(generations)-1].Observations[0]
			if kind == credentials.SourceErrorUnavailable {
				require.Contains(t, published, provider.ID)
				require.True(t, last.Usable)
				require.Equal(t, providerstate.ReasonOperatorRefreshRetained, last.Reason)
			} else {
				require.NotContains(t, published, provider.ID)
				require.False(t, last.Usable)
			}
		})
	}
}

func TestTerminalRefreshFailureRemainsVisibleWhenPublicationFails(t *testing.T) {
	provider := reconcilerTestProvider("provider")
	failed := false
	resolver := &reconcilerTestResolver{resolve: func(context.Context, catalogs.Provider) (config.ProviderConfig, bool, error) {
		if failed {
			return config.ProviderConfig{}, false, credentials.NewSourceError(credentials.SourceErrorDenied, "test")
		}
		return reconcilerProviderConfig(provider), true, nil
	}}
	states := &credentialStateCapture{}
	view := reconcilerTestView(provider)
	reconciler, err := NewReconciler(func() (CatalogView, error) { return view, nil }, resolver, nil, func(context.Context, CatalogView, config.ProvidersConfig) error {
		if failed {
			return context.DeadlineExceeded
		}
		return nil
	}, time.Second, states)
	require.NoError(t, err)
	_, err = reconciler.Reconcile(t.Context(), false)
	require.NoError(t, err)
	failed = true
	_, err = reconciler.Reconcile(t.Context(), true)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	generations := states.snapshot()
	last := generations[len(generations)-1].Observations[0]
	require.False(t, last.Usable)
	require.Equal(t, providerstate.CredentialDenied, last.State)
}
