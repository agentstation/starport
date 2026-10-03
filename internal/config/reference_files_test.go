package config

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/stretchr/testify/require"
)

// recordingLookuper records each key that go-envconfig reads and returns no
// value.
type recordingLookuper struct {
	keys map[string]bool
}

func (r *recordingLookuper) Lookup(key string) (string, bool) {
	r.keys[key] = true
	return "", false
}

func TestReferenceFilesMatchCommittedFiles(t *testing.T) {
	// Values in the process environment must not reach the reference files.
	t.Setenv("STARPORT_CONFIG_DIR", t.TempDir())
	t.Setenv("STARPORT_LOGGING_LEVEL", "debug")
	t.Setenv("STARPORT_STORAGE_SQL_SQLITE_PATH", filepath.Join(t.TempDir(), "selected.db"))
	files, err := ReferenceFiles(t.Context())
	require.NoError(t, err)
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.Name)
		committed, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(ReferenceDirectory), file.Name))
		require.NoError(t, err, "run make docs-generate")
		require.Equal(t, string(committed), string(file.Data), "%s is stale; run make docs-generate", file.Name)
	}
	entries, err := os.ReadDir(filepath.Join("..", "..", filepath.FromSlash(ReferenceDirectory)))
	require.NoError(t, err)
	for _, entry := range entries {
		require.Contains(t, names, entry.Name(), "the generator does not write %s", entry.Name())
	}
}

func TestReferenceFilesHoldNoHostPath(t *testing.T) {
	files, err := ReferenceFiles(t.Context())
	require.NoError(t, err)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	for _, file := range files {
		require.NotContains(t, string(file.Data), "starport-reference-", file.Name)
		require.False(t, bytes.Contains(file.Data, []byte(home)), "%s holds the host home directory", file.Name)
		require.False(t, bytes.Contains(file.Data, []byte(filepath.ToSlash(home))), "%s holds the host home directory", file.Name)
	}
}

func TestSettingsInventoryNamesTheVariablesThatTheLoaderReads(t *testing.T) {
	recorder := &recordingLookuper{keys: make(map[string]bool)}
	require.NoError(t, NewLoader().decode(t.Context(), &Config{}, recorder))
	read := make([]string, 0, len(recorder.keys))
	for key := range recorder.keys {
		read = append(read, key)
	}
	slices.Sort(read)

	inventory := newSettingsInventory()
	listed := make([]string, 0, len(inventory.Environment))
	for _, setting := range inventory.Environment {
		listed = append(listed, setting.Name)
	}
	slices.Sort(listed)
	require.Equal(t, read, slices.Compact(slices.Clone(listed)))
	require.Len(t, listed, len(read), "the inventory lists a variable more than once")
}

func TestSettingsInventoryMarksSecretsAndDefaultSources(t *testing.T) {
	inventory := newSettingsInventory()
	settings := make(map[string]environmentSetting, len(inventory.Environment))
	for _, setting := range inventory.Environment {
		settings[setting.Name] = setting
	}
	require.Equal(t, redactionValue, settings["STARPORT_SECURITY_MASTER_KEY"].Redaction)
	require.True(t, settings["STARPORT_SECURITY_MASTER_KEY"].Secret)
	require.Equal(t, redactionURL, settings["STARPORT_STORAGE_SQL_POSTGRES_URL"].Redaction)
	require.Equal(t, defaultSourcePlatformPath, settings["STARPORT_FILES_PATH"].DefaultSource)
	require.Empty(t, settings["STARPORT_FILES_PATH"].Default)
	require.Equal(t, defaultSourceTag, settings["STARPORT_LOGGING_LEVEL"].DefaultSource)
	require.Equal(t, "info", settings["STARPORT_LOGGING_LEVEL"].Default)
	require.False(t, settings["STARPORT_LOGGING_LEVEL"].Secret)

	schema := ConfigurationSchema()
	require.Len(t, inventory.Schema, len(schema))
	for index, setting := range inventory.Schema {
		require.Equal(t, schema[index], setting.SchemaSetting)
		// Starmap decodes some catalog settings itself. They have no env tag.
		if environment, ok := settings[setting.Environment]; ok {
			require.Equal(t, setting.ID, environment.SchemaID, setting.Environment)
		}
	}
}

func TestReferencePlatformMatchesUserDefaults(t *testing.T) {
	home, appData, localAppData := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", appData)
	t.Setenv("LOCALAPPDATA", localAppData)
	for _, name := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(name, "")
	}
	index := slices.IndexFunc(referencePlatforms, func(platform referencePlatform) bool { return platform.ID == runtime.GOOS })
	if index < 0 {
		t.Skipf("the release does not build %s", runtime.GOOS)
	}
	platform := referencePlatforms[index]
	expand := strings.NewReplacer("~", home, "%AppData%", appData, "%LocalAppData%", localAppData)
	defaults := productpaths.UserDefaults(productpaths.Starport)
	for _, root := range referenceRoots {
		want, err := defaults(root)
		require.NoError(t, err)
		require.Equal(t, filepath.Clean(want), filepath.Clean(expand.Replace(platform.Roots[root])), string(root))
	}
}

func TestReferencePlatformManifestRewritesEveryPlaceholder(t *testing.T) {
	placeholder, err := defaultFileManifest(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, placeholder.Files)
	for _, platform := range referencePlatforms {
		report := platform.manifest(placeholder)
		for root, path := range report.Roots {
			require.Equal(t, platform.Roots[root], path.Path, "%s %s", platform.ID, root)
		}
		for _, entry := range report.Files {
			for _, location := range []string{entry.Location.Path, entry.Location.Anchor} {
				require.NotContains(t, location, "starport-reference-", "%s %s", platform.ID, entry.ID)
			}
		}
		configuration := manifestEntry(t, report, "configuration")
		require.Equal(t, platform.Roots[productpaths.Config]+platform.Separator+"config.env", configuration.Location.Path)
	}
}
