package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
)

func docsSite() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                           &fstest.MapFile{Data: []byte("<!doctype html>docs")},
		"manifest.json":                        &fstest.MapFile{Data: []byte(`{"starport_release":"v1.3.0","files":{}}`)},
		"operate-starport/identity/index.html": &fstest.MapFile{Data: []byte("<!doctype html>identity")},
		"assets/docs.css":                      &fstest.MapFile{Data: []byte("body{}")},
	}
}

// docsDependencies returns dependencies whose configuration, path, and
// session boundaries fail, so a docs export that uses one of them fails.
func docsDependencies(t *testing.T, site fs.FS) (Dependencies, func() string) {
	t.Helper()
	deps, stdout, _ := testDependencies()
	deps.LoadConfig = func(context.Context) (*config.Config, error) {
		t.Error("docs export loaded the configuration")
		return nil, errors.New("configuration is not available")
	}
	deps.ResolvePaths = func() (config.Paths, error) {
		t.Error("docs export resolved the product paths")
		return config.Paths{}, errors.New("paths are not available")
	}
	if site != nil {
		deps.Docs = func() (fs.FS, bool) { return site, true }
	}
	return deps, stdout.String
}

func TestDocsExportWritesTheSite(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "site")
	deps, stdout := docsDependencies(t, docsSite())
	require.NoError(t, Run(t.Context(), []string{"starport", "docs", "export", directory}, deps))
	require.Equal(t, "Exported 4 files to "+directory+"\nRelease: v1.3.0\n", stdout())
	require.NoError(t, fstest.TestFS(os.DirFS(directory), "index.html", "manifest.json", "operate-starport/identity/index.html", "assets/docs.css"))
	for path, file := range docsSite() {
		data, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(path)))
		require.NoError(t, err)
		require.Equal(t, file.Data, data, path)
	}
}

func TestDocsExportRefusesANonEmptyDirectory(t *testing.T) {
	directory := t.TempDir()
	existing := filepath.Join(directory, "index.html")
	require.NoError(t, os.WriteFile(existing, []byte("operator page"), 0o600))
	deps, stdout := docsDependencies(t, docsSite())
	err := Run(t.Context(), []string{"starport", "docs", "export", directory}, deps)
	require.ErrorContains(t, err, "is not empty; use --force")
	require.Equal(t, ExitCodeRuntime, ExitCode(err))
	require.Empty(t, stdout())
	data, err := os.ReadFile(existing)
	require.NoError(t, err)
	require.Equal(t, "operator page", string(data))
	_, err = os.Stat(filepath.Join(directory, "manifest.json"))
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestDocsExportForceReplacesFilesWithTheSamePath(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "index.html"), []byte("old page"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "notes.txt"), []byte("operator notes"), 0o600))
	deps, stdout := docsDependencies(t, docsSite())
	require.NoError(t, Run(t.Context(), []string{"starport", "docs", "export", "--force", directory}, deps))
	require.Contains(t, stdout(), "Exported 4 files to ")
	data, err := os.ReadFile(filepath.Join(directory, "index.html"))
	require.NoError(t, err)
	require.Equal(t, "<!doctype html>docs", string(data))
	data, err = os.ReadFile(filepath.Join(directory, "notes.txt"))
	require.NoError(t, err)
	require.Equal(t, "operator notes", string(data))
}

func TestDocsExportRefusesABuildWithoutDocs(t *testing.T) {
	for name, source := range map[string]DocsSource{
		"no source": nil,
		"not built": func() (fs.FS, bool) { return nil, false },
	} {
		t.Run(name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "site")
			deps, stdout := docsDependencies(t, nil)
			deps.Docs = source
			err := Run(t.Context(), []string{"starport", "docs", "export", directory}, deps)
			require.EqualError(t, err, docsNotBuilt)
			require.Equal(t, ExitCodeRuntime, ExitCode(err))
			require.Empty(t, stdout())
			_, err = os.Stat(directory)
			require.ErrorIs(t, err, fs.ErrNotExist)
		})
	}
}

func TestDocsExportReportsAnUnknownRelease(t *testing.T) {
	for name, manifest := range map[string]*fstest.MapFile{
		"no manifest":      nil,
		"invalid manifest": {Data: []byte("{")},
		"no release":       {Data: []byte(`{"files":{}}`)},
	} {
		t.Run(name, func(t *testing.T) {
			site := docsSite()
			delete(site, "manifest.json")
			if manifest != nil {
				site["manifest.json"] = manifest
			}
			directory := filepath.Join(t.TempDir(), "site")
			deps, stdout := docsDependencies(t, site)
			require.NoError(t, Run(t.Context(), []string{"starport", "docs", "export", directory}, deps))
			require.Contains(t, stdout(), "\nRelease: unknown\n")
		})
	}
}

func TestDocsExportRequiresOneDirectory(t *testing.T) {
	for name, args := range map[string][]string{
		"no directory":    {"starport", "docs", "export"},
		"two directories": {"starport", "docs", "export", "one", "two"},
	} {
		t.Run(name, func(t *testing.T) {
			deps, _ := docsDependencies(t, docsSite())
			err := Run(t.Context(), args, deps)
			require.ErrorContains(t, err, "requires one directory argument")
			require.Equal(t, ExitCodeUsage, ExitCode(err))
		})
	}
}

func TestDocsExportRefusesAFileAsTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("file"), 0o600))
	deps, _ := docsDependencies(t, docsSite())
	err := Run(t.Context(), []string{"starport", "docs", "export", "--force", path}, deps)
	require.ErrorContains(t, err, "export directory")
	require.Equal(t, ExitCodeRuntime, ExitCode(err))
}
