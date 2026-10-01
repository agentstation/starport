package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRecoveryActivationSealedRetryRequiresOriginalSelector(t *testing.T) {
	cfg, request := activationFleetFixture(t)
	sealed, native := sealActivationFixture(t, cfg, request)
	require.NoError(t, native.close())
	decisionPath := filepath.Join(request.ActivationDirectory, "decision.json")
	original, err := os.ReadFile(decisionPath)
	require.NoError(t, err)
	wrong := strings.Repeat("f", 64)
	require.NotEqual(t, sealed.journal.Digest(), wrong)
	for _, test := range []struct{ name, selector string }{{"missing", ""}, {"mismatched", wrong}} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			request.ExpectedDecisionSHA256 = test.selector
			_, err := ActivateRecovery(ctx, cfg, request)
			require.ErrorIs(t, err, recovery.ErrConflict)
			current, err := os.ReadFile(decisionPath)
			require.NoError(t, err)
			require.Equal(t, original, current)
			journal, err := recovery.InspectActivationJournal(ctx, request.ActivationDirectory, sealed.journal.Digest())
			require.NoError(t, err)
			require.Zero(t, journal.CompletedPhases())
			native, err := openRecoveryActivationNative(ctx, cfg, request)
			require.NoError(t, err)
			defer func() { require.NoError(t, native.close()) }()
			boundary, err := native.witness.Current(ctx, sealed.facts.Boundary.DeploymentID)
			require.NoError(t, err)
			require.Equal(t, sealed.facts.Boundary, boundary)
			require.ErrorIs(t, native.db.CheckImportBarrier(ctx), sqlstore.ErrImportRestricted)
			require.NoError(t, native.kv.(storage.ImportUnreleasedInspector).CheckUnreleasedImport(ctx, sealed.identity.KVClaim))
			require.NoError(t, native.blobs.(blob.ImportUnreleasedInspector).CheckUnreleasedImport(ctx, sealed.identity.ComponentOperation, sealed.identity.BlobOriginal))
		})
	}
}
