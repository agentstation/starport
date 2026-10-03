package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSaveTargetNamesTheFileTheRevisionOrTheController(t *testing.T) {
	local, file := loadAuthorityConfig(t, nil, "STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n")
	require.Equal(t, SaveTarget{Kind: SaveTargetLocalFile, Path: file}, local.EffectiveReport().Target)

	shared, _ := loadAuthorityConfig(t, map[string]string{managementEnvironment: ManagementShared}, "")
	require.Equal(t, SaveTarget{Kind: SaveTargetSharedRevision}, shared.EffectiveReport().Target)

	external, _ := loadAuthorityConfig(t, map[string]string{managementEnvironment: ManagementExternal}, "")
	require.Equal(t, SaveTarget{Kind: SaveTargetExternalController}, external.EffectiveReport().Target)
}

func TestSaveTargetReportsWhyLayeredFilesRefuseALocalSave(t *testing.T) {
	paths := PathsForConfigDir(t.TempDir())
	first, second := filepath.Join(paths.ConfigDir, "first.env"), filepath.Join(paths.ConfigDir, "second.env")
	require.NoError(t, os.WriteFile(first, []byte("STARPORT_CATALOG_ACQUISITION_INTERVAL=7m\n"), 0o600))
	require.NoError(t, os.WriteFile(second, []byte("STARPORT_CATALOG_ACQUISITION_INTERVAL=9m\n"), 0o600))
	cfg, err := NewLoader().WithPaths(paths).WithEnvironment(nil).WithEnvFiles(first, second).Load(t.Context())
	require.NoError(t, err)

	target := cfg.EffectiveReport().Target
	require.Equal(t, SaveTargetLocalFile, target.Kind)
	require.Empty(t, target.Path)
	_, refusal := cfg.localSaveFile()
	require.NotNil(t, refusal)
	require.Equal(t, refusal.Message, target.Unavailable, "the report and the save give one reason")
	require.Empty(t, cfg.AppliedRevision().FileChecksum)
}
