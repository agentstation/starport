package recovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/stretchr/testify/require"
)

func TestVerifiedSourceRechecksOriginalArtifactsAndKeyAccess(t *testing.T) {
	for _, test := range []struct{ name, artifact string }{
		{"unchanged", ""}, {bundleKVFile, bundleKVFile}, {"relational-image", bundleSQLFile},
		{"selected-configuration", "files/configuration/config.env"}, {bundleManifestFile, bundleManifestFile}, {"unexpected-artifact", "extra.json"},
		{"encryption-key", ""}, {"changed-selection", ""}, {"cancelled-context", ""}, {"uninitialized-source", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, request, directory := backupBundleFixture(t)
			manifest, err := BackupBundle(t.Context(), directory, source, request)
			require.NoError(t, err)
			digest, err := manifest.Digest()
			require.NoError(t, err)
			selection := VerifyRequest{Directory: directory, ManifestSHA256: digest}
			verified, err := InspectRestoreSource(t.Context(), selection, source.Encryption)
			require.NoError(t, err)
			before := verified.References()
			ctx, encryption := t.Context(), source.Encryption
			if test.artifact != "" {
				require.NoError(t, os.WriteFile(filepath.Join(directory, test.artifact), []byte("changed original evidence"), 0600))
			}
			switch test.name {
			case "encryption-key":
				encryption, err = credentials.NewEncryptionService([]byte(strings.Repeat("x", 32)))
				require.NoError(t, err)
			case "changed-selection":
				selection.Directory = filepath.Dir(directory)
			case "cancelled-context":
				canceled, cancel := context.WithCancel(t.Context())
				cancel()
				ctx = canceled
			case "uninitialized-source":
				verified = &RestoreSource{}
			}
			err = verified.CheckOriginalArtifacts(ctx, selection, encryption)
			if test.name == "unchanged" {
				require.NoError(t, err)
				require.Equal(t, before, verified.References())
			} else {
				require.Error(t, err)
			}
		})
	}
	var absent *RestoreSource
	require.ErrorIs(t, absent.CheckOriginalArtifacts(t.Context(), VerifyRequest{}, nil), ErrConflict)
}
