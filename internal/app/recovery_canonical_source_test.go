package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRecoveryCanonicalCheckedSourceRefusesChangedOriginalBeforeRelease(t *testing.T) {
	cfg, request, publication := canonicalRoleFixture(t, config.InferenceCredentialPolicyRole)
	originals := canonicalAllOriginals(t, cfg, request, publication)
	source, _, _, err := inspectBackupRestore(t.Context(), cfg, request.PrepareRequest)
	require.NoError(t, err)
	proof, err := verifyCanonicalFilesWithSource(t.Context(), cfg, request.PrepareRequest, originals, false, source)
	require.NoError(t, err)
	body, err := proof.Record()
	require.NoError(t, err)
	checked, err := inspectCanonicalFilesWithSource(t.Context(), cfg, request.PrepareRequest, body, proof.Digest(), source)
	require.NoError(t, err)
	again, err := checked.Record()
	require.NoError(t, err)
	require.Equal(t, body, again)
	requirePublicationClosedSQLAndKV(t, cfg)

	require.NoError(t, os.WriteFile(filepath.Join(request.Directory, "kv.db"), []byte("changed original snapshot"), 0600))
	_, err = inspectCanonicalFilesWithSource(t.Context(), cfg, request.PrepareRequest, body, proof.Digest(), source)
	require.Error(t, err)
	requirePublicationClosedSQLAndKV(t, cfg)
}
