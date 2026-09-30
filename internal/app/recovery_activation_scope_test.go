package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

// TestRecoveryActivationReleasesDecodedCatalogScope proves that ActivateRecovery
// closes its decoded catalog scope on a refusal and on completion.
// Equal payload bytes decode to one catalog inside an open scope and to distinct catalogs outside one.
func TestRecoveryActivationReleasesDecodedCatalogScope(t *testing.T) {
	cfg, prepare := boundedActivationSourceFixture(t)
	cfg, request := activationPreparedFixture(t, cfg, prepare)
	payload, err := catalogs.EncodeCatalogPayload(syntheticInferenceCatalog(t, "https://scope.invalid"))
	require.NoError(t, err)
	requireDecodedCatalogScopeClosed(t, payload)

	release := catalogs.RetainDecodedCatalogs()
	first, err := catalogs.DecodeCatalogPayload(payload)
	require.NoError(t, err)
	again, err := catalogs.DecodeCatalogPayload(payload)
	require.NoError(t, err)
	require.Same(t, first, again)
	release()
	requireDecodedCatalogScopeClosed(t, payload)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	refused := request
	refused.ExpectedDecisionSHA256 = strings.Repeat("f", 64)
	_, err = ActivateRecovery(ctx, cfg, refused)
	require.ErrorIs(t, err, recovery.ErrConflict)
	requireDecodedCatalogScopeClosed(t, payload)

	result, err := ActivateRecovery(ctx, cfg, request)
	require.NoError(t, err)
	require.True(t, result.HistoricallyComplete)
	requireDecodedCatalogScopeClosed(t, payload)
}

func requireDecodedCatalogScopeClosed(t *testing.T, payload []byte) {
	t.Helper()
	first, err := catalogs.DecodeCatalogPayload(payload)
	require.NoError(t, err)
	second, err := catalogs.DecodeCatalogPayload(payload)
	require.NoError(t, err)
	require.NotSame(t, first, second, "a decoded catalog scope stayed open")
}
