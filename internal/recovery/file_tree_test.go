package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/stretchr/testify/require"
)

func TestRestoreFileTreePublishesOnlyAfterOwnerValidation(t *testing.T) {
	source, capture, backup := backupBundleFixture(t)
	manifest, err := BackupBundle(t.Context(), backup, source, capture)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	verified, err := InspectRestoreSource(t.Context(), VerifyRequest{Directory: backup, ManifestSHA256: digest}, source.Encryption)
	require.NoError(t, err)
	target := filepath.Join(privateKVDirectory(t), "restored")
	input := filepath.Join(backup, "files/configuration/config.env")
	expected, err := os.ReadFile(input)
	require.NoError(t, err)
	request := FileTreeRequest{Destination: target, Files: []FileTreeFile{{ArtifactID: "configuration/config.env", Relative: "nested/模型.env"}}}
	checked := 0
	validate := func(ctx context.Context, directory string) error {
		checked++
		body, err := os.ReadFile(filepath.Join(directory, "nested/模型.env"))
		require.NoError(t, err)
		require.Equal(t, expected, body)
		if directory != target {
			require.NoDirExists(t, target)
		}
		return ctx.Err()
	}
	result, err := verified.PublishFileTree(t.Context(), request, validate)
	require.NoError(t, err)
	require.True(t, result.Published)
	require.False(t, result.Reused)
	require.NotEmpty(t, result.DirectoryIdentity)
	require.Equal(t, 1, checked)
	again, err := verified.PublishFileTree(t.Context(), request, validate)
	require.NoError(t, err)
	require.True(t, again.Reused)
	require.Equal(t, result.DirectoryIdentity, again.DirectoryIdentity)
	require.Equal(t, result.SelectionSHA256, again.SelectionSHA256)
	require.Equal(t, 2, checked)
}

func restoreFileTreeFixture(t *testing.T) (*RestoreSource, FileTreeRequest) {
	t.Helper()
	source, capture, backup := backupBundleFixture(t)
	source.Files = append(source.Files, BundleFile{ID: "configuration/second.env", Path: source.Files[0].Path})
	manifest, err := BackupBundle(t.Context(), backup, source, capture)
	require.NoError(t, err)
	digest, err := manifest.Digest()
	require.NoError(t, err)
	verified, err := InspectRestoreSource(t.Context(), VerifyRequest{Directory: backup, ManifestSHA256: digest}, source.Encryption)
	require.NoError(t, err)
	return verified, FileTreeRequest{Destination: filepath.Join(privateKVDirectory(t), "restored"), Files: []FileTreeFile{{ArtifactID: "configuration/config.env", Relative: "config.env"}}}
}

func acceptTestTree(ctx context.Context, _ string) error { return ctx.Err() }

func TestRestoreFileTreePreservesConflictingTargets(t *testing.T) {
	for _, mode := range []string{"bytes", "extra-file", "extra-directory", "symlink", "wrong-type", "owner-refusal"} {
		t.Run(mode, func(t *testing.T) {
			source, request := restoreFileTreeFixture(t)
			original, err := source.PublishFileTree(t.Context(), request, acceptTestTree)
			require.NoError(t, err)
			name := filepath.Join(request.Destination, "config.env")
			validator := FileTreeValidator(acceptTestTree)
			switch mode {
			case "bytes":
				require.NoError(t, os.WriteFile(name, []byte("operator update"), 0o600))
			case "extra-file":
				require.NoError(t, os.WriteFile(filepath.Join(request.Destination, "operator-note"), []byte("keep"), 0o600))
			case "extra-directory":
				require.NoError(t, os.Mkdir(filepath.Join(request.Destination, "operator-data"), 0o700))
			case "symlink":
				require.NoError(t, os.Rename(name, name+".original"))
				require.NoError(t, os.Symlink("config.env.original", name))
			case "wrong-type":
				require.NoError(t, os.Rename(name, name+".original"))
				require.NoError(t, os.Mkdir(name, 0o700))
			case "owner-refusal":
				validator = func(context.Context, string) error { return errors.New("owner refuses old policy") }
			}
			again, err := source.PublishFileTree(t.Context(), request, validator)
			require.Error(t, err)
			require.False(t, again.Published)
			directory, err := productfiles.ExistingDirectory(request.Destination)
			require.NoError(t, err)
			identity, err := directory.Identity()
			require.NoError(t, err)
			require.Equal(t, original.DirectoryIdentity, identity)
			if mode == "bytes" {
				body, err := os.ReadFile(name)
				require.NoError(t, err)
				require.Equal(t, "operator update", string(body))
			}
		})
	}
}

func TestRestoreFileTreeFailureBeforePublicationKeepsTargetAbsent(t *testing.T) {
	for _, mode := range []string{"source-changed", "owner-refusal", "owner-mutation", "canceled", "missing-validator"} {
		t.Run(mode, func(t *testing.T) {
			source, request := restoreFileTreeFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			validator := FileTreeValidator(acceptTestTree)
			switch mode {
			case "source-changed":
				require.NoError(t, os.WriteFile(filepath.Join(source.request.Directory, "files/configuration/config.env"), []byte("wrong"), 0o600))
			case "owner-refusal":
				validator = func(context.Context, string) error { return errors.New("owner rejected") }
			case "owner-mutation":
				validator = func(_ context.Context, directory string) error {
					return os.WriteFile(filepath.Join(directory, "config.env"), []byte("modified by validator"), 0o600)
				}
			case "canceled":
				validator = func(context.Context, string) error { cancel(); return nil }
			case "missing-validator":
				validator = nil
			}
			result, err := source.PublishFileTree(ctx, request, validator)
			require.Error(t, err)
			require.False(t, result.Published)
			require.NoDirExists(t, request.Destination)
			entries, err := os.ReadDir(filepath.Dir(request.Destination))
			require.NoError(t, err)
			require.Empty(t, entries, "owned temporary staging must be removed on a returned failure")
		})
	}
}

func TestRestoreFileTreeRejectsUnsafeSelectionBeforeStaging(t *testing.T) {
	source, original := restoreFileTreeFixture(t)
	for _, mode := range []string{"relative-target", "source-overlap", "empty", "unknown", "traversal", "case-alias", "file-parent", "duplicate-payload"} {
		t.Run(mode, func(t *testing.T) {
			request := original
			request.Files = slices.Clone(original.Files)
			switch mode {
			case "relative-target":
				request.Destination = "relative"
			case "source-overlap":
				request.Destination = filepath.Join(source.request.Directory, "target")
			case "empty":
				request.Files = nil
			case "unknown":
				request.Files[0].ArtifactID = "unknown"
			case "traversal":
				request.Files[0].Relative = "../escape"
			case "case-alias":
				request.Files = append(request.Files, FileTreeFile{ArtifactID: "configuration/second.env", Relative: "CONFIG.env"})
			case "file-parent":
				request.Files = append(request.Files, FileTreeFile{ArtifactID: "configuration/second.env", Relative: "config.env/child"})
			case "duplicate-payload":
				request.Files = append(request.Files, request.Files[0])
			}
			_, err := source.PublishFileTree(t.Context(), request, acceptTestTree)
			require.Error(t, err)
			entries, err := os.ReadDir(filepath.Dir(original.Destination))
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestRestoreFileTreeResumesAfterLostPublicationAcknowledgment(t *testing.T) {
	source, request := restoreFileTreeFixture(t)
	lost := errors.New("process stopped after namespace publication")
	first, err := source.publishFileTree(t.Context(), request, acceptTestTree, func(phase string) error {
		if phase == "published" {
			return lost
		}
		return nil
	})
	require.ErrorIs(t, err, lost)
	require.True(t, first.Published)
	require.DirExists(t, request.Destination)
	recovered, err := source.PublishFileTree(t.Context(), request, acceptTestTree)
	require.NoError(t, err)
	require.True(t, recovered.Reused)
	require.Equal(t, first.DirectoryIdentity, recovered.DirectoryIdentity)
	require.Equal(t, first.SelectionSHA256, recovered.SelectionSHA256)
}

func TestRestoreFileTreePreservesACompetingDirectory(t *testing.T) {
	source, request := restoreFileTreeFixture(t)
	_, err := source.publishFileTree(t.Context(), request, acceptTestTree, func(phase string) error {
		if phase == "validated" {
			_, err := productfiles.CreateDirectory(request.Destination)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(request.Destination, "operator-file"), []byte("keep"), 0o600)
		}
		return nil
	})
	require.Error(t, err)
	bytes, err := os.ReadFile(filepath.Join(request.Destination, "operator-file"))
	require.NoError(t, err)
	require.Equal(t, "keep", string(bytes))
	require.NoFileExists(t, filepath.Join(request.Destination, "config.env"))
}

func TestRestoreFileTreeRejectsStagingReplacementWithoutDeletingIt(t *testing.T) {
	source, request := restoreFileTreeFixture(t)
	staging := ""
	validate := func(_ context.Context, directory string) error { staging = directory; return nil }
	result, err := source.publishFileTree(t.Context(), request, validate, func(phase string) error {
		if phase != "validated" {
			return nil
		}
		if err := os.Rename(staging, staging+"-preserved"); err != nil {
			return err
		}
		if _, err := productfiles.CreateDirectory(staging); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(staging, "operator-file"), []byte("keep"), 0o600)
	})
	require.Error(t, err)
	require.False(t, result.Published)
	require.NoDirExists(t, request.Destination)
	body, err := os.ReadFile(filepath.Join(staging, "operator-file"))
	require.NoError(t, err)
	require.Equal(t, "keep", string(body))
	require.FileExists(t, filepath.Join(staging+"-preserved", "config.env"))
}

func TestRestoreFileTreeConcurrentPublicationHasOneWinner(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	source, request := restoreFileTreeFixture(t)
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make([]FileTreeResult, 2)
	failures := make([]error, 2)
	var group sync.WaitGroup
	for i := range 2 {
		group.Go(func() {
			results[i], failures[i] = source.PublishFileTree(ctx, request, func(ctx context.Context, _ string) error {
				ready <- struct{}{}
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		})
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			group.Wait()
			t.Fatal(ctx.Err())
		}
	}
	close(release)
	group.Wait()
	published := 0
	for i := range 2 {
		if failures[i] == nil {
			require.True(t, results[i].Published)
			published++
		} else {
			require.False(t, results[i].Published)
		}
	}
	require.Equal(t, 1, published)
	result, err := source.PublishFileTree(t.Context(), request, acceptTestTree)
	require.NoError(t, err)
	require.True(t, result.Reused)
}

func TestRestoreFileTreePortableNames(t *testing.T) {
	for _, name := range []string{"models/模型.yaml", "record", "a.b/c-d"} {
		require.True(t, validRestoreTreeName(name), name)
	}
	for _, name := range []string{"", ".", "..", "a/../b", "a\\b", "a:b", "a?b", "a\nb", "CON.txt", "a/b.", "a/b "} {
		require.False(t, validRestoreTreeName(name), name)
	}
}
