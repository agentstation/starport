package recovery

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestCanonicalFileInspectionRetainedRestart(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "local"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			native, capture, backup := backupBundleRecipe(t, shared)
			manifest, err := BackupBundle(t.Context(), backup, native, capture)
			require.NoError(t, err)
			digest, err := manifest.Digest()
			require.NoError(t, err)
			verify := VerifyRequest{Directory: backup, ManifestSHA256: digest}
			source, err := InspectRestoreSource(t.Context(), verify, native.Encryption)
			require.NoError(t, err)
			request := FileTreeRequest{Destination: filepath.Join(privateKVDirectory(t), "original"), Files: []FileTreeFile{{ArtifactID: "configuration/config.env", Relative: "config.env"}}}
			original, err := source.PublishFileTree(t.Context(), request, acceptTestTree)
			require.NoError(t, err)
			require.NotEmpty(t, original.ParentIdentity)
			proof, err := source.InspectPublishedFileTree(t.Context(), request, original, acceptTestTree)
			require.NoError(t, err)
			record, err := proof.Record()
			require.NoError(t, err)
			sealed := proof.Digest()
			// A component release removes its original import barrier. File inspection
			// must not reconstruct preparation or require that removed barrier.
			kind := storage.StorageTypeBadger
			if shared {
				kind = storage.StorageTypeValkey
			}
			store, transfer, _ := kvTransferStores(t, kind)
			claim := []byte("file-inspection-after-release")
			require.NoError(t, transfer.Claim(t.Context(), claim))
			require.NoError(t, transfer.(storage.ImportReplayActivator).ActivateImportAt(t.Context(), claim, storage.ImportReplayPosition{}, strings.Repeat("a", 64)))
			before, err := os.Stat(request.Destination)
			require.NoError(t, err)
			fileBefore, err := os.Stat(filepath.Join(request.Destination, "config.env"))
			require.NoError(t, err)
			reloaded, err := InspectRestoreSource(t.Context(), verify, native.Encryption)
			require.NoError(t, err)
			restarted, err := reloaded.InspectRetainedFileTree(t.Context(), request, record, sealed, acceptTestTree)
			require.NoError(t, err)
			again, err := restarted.Record()
			require.NoError(t, err)
			require.Equal(t, record, again)
			require.Equal(t, sealed, restarted.Digest())
			require.NoError(t, restarted.Check(t.Context(), reloaded, request, acceptTestTree))
			after, err := os.Stat(request.Destination)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
			require.Equal(t, before.ModTime(), after.ModTime())
			fileAfter, err := os.Stat(filepath.Join(request.Destination, "config.env"))
			require.NoError(t, err)
			require.True(t, os.SameFile(fileBefore, fileAfter))
			require.Equal(t, fileBefore.ModTime(), fileAfter.ModTime())
			require.NoDirExists(t, filepath.Join(request.Destination, productfiles.PublicationDirectoryName))
			require.NoError(t, storage.CheckImportBarrier(t.Context(), store))
			witness, err := New(native.SQL)
			require.NoError(t, err)
			boundary, err := witness.Current(t.Context(), capture.Boundary.DeploymentID)
			require.NoError(t, err)
			require.False(t, boundary.Open, "file inspection grants no SQL approval")
		})
	}
}

func TestCanonicalFileInspectionRefusesMissingOriginalEvidence(t *testing.T) {
	source, request := restoreFileTreeFixture(t)
	original, err := source.PublishFileTree(t.Context(), request, acceptTestTree)
	require.NoError(t, err)
	for _, kind := range []string{"parent", "tree", "selection", "destination", "files", "uncompleted", "both-outcomes"} {
		t.Run(kind, func(t *testing.T) {
			changed := original
			switch kind {
			case "parent":
				changed.ParentIdentity = ""
			case "tree":
				changed.DirectoryIdentity = ""
			case "selection":
				changed.SelectionSHA256 = strings.Repeat("b", 64)
			case "destination":
				changed.Destination = filepath.Join(privateKVDirectory(t), "different")
			case "files":
				changed.Files++
			case "uncompleted":
				changed.Published = false
				changed.Reused = false
			case "both-outcomes":
				changed.Published = true
				changed.Reused = true
			}
			proof, err := source.InspectPublishedFileTree(t.Context(), request, changed, acceptTestTree)
			require.Error(t, err)
			require.Nil(t, proof)
		})
	}
	require.NoDirExists(t, filepath.Join(request.Destination, productfiles.PublicationDirectoryName))
}

func TestCanonicalFileInspectionRefusesChangedTree(t *testing.T) {
	for _, kind := range []string{"bytes", "extra", "missing", "identity", "owner-refusal", "owner-change", "pending-parent", "pending-tree"} {
		t.Run(kind, func(t *testing.T) {
			source, request := restoreFileTreeFixture(t)
			original, err := source.PublishFileTree(t.Context(), request, acceptTestTree)
			require.NoError(t, err)
			proof, err := source.InspectPublishedFileTree(t.Context(), request, original, acceptTestTree)
			require.NoError(t, err)
			record, err := proof.Record()
			require.NoError(t, err)
			file := filepath.Join(request.Destination, "config.env")
			validator := FileTreeValidator(acceptTestTree)
			switch kind {
			case "bytes":
				require.NoError(t, os.WriteFile(file, []byte("different"), 0600))
			case "extra":
				require.NoError(t, os.WriteFile(filepath.Join(request.Destination, "extra"), []byte("unexpected"), 0600))
			case "missing":
				require.NoError(t, os.Remove(file))
			case "identity":
				body, err := os.ReadFile(file)
				require.NoError(t, err)
				require.NoError(t, os.Rename(request.Destination, request.Destination+"-original"))
				_, err = productfiles.CreateDirectory(request.Destination)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(file, body, 0600))
			case "owner-refusal":
				validator = func(context.Context, string) error { return errors.New("semantic owner refuses the selected policy") }
			case "owner-change":
				validator = func(_ context.Context, path string) error {
					return os.WriteFile(filepath.Join(path, "config.env"), []byte("changed by invalid validator"), 0600)
				}
			case "pending-parent", "pending-tree":
				path := request.Destination
				if kind == "pending-parent" {
					path = filepath.Dir(path)
				}
				metadata := filepath.Join(path, productfiles.PublicationDirectoryName)
				require.NoError(t, os.Mkdir(metadata, 0700))
				require.NoError(t, os.WriteFile(filepath.Join(metadata, "transaction.json"), []byte("unresolved"), 0600))
			}
			reopened, err := source.InspectRetainedFileTree(t.Context(), request, record, proof.Digest(), validator)
			require.Error(t, err)
			require.Nil(t, reopened)
			if kind == "pending-parent" || kind == "pending-tree" {
				path := request.Destination
				if kind == "pending-parent" {
					path = filepath.Dir(path)
				}
				body, err := os.ReadFile(filepath.Join(path, productfiles.PublicationDirectoryName, "transaction.json"))
				require.NoError(t, err)
				require.Equal(t, "unresolved", string(body))
			}
		})
	}
}

func TestCanonicalFileInspectionRetainsOriginalParent(t *testing.T) {
	source, request := restoreFileTreeFixture(t)
	base := privateKVDirectory(t)
	parent := filepath.Join(base, "parent")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	request.Destination = filepath.Join(parent, "canonical")
	original, err := source.PublishFileTree(t.Context(), request, acceptTestTree)
	require.NoError(t, err)
	proof, err := source.InspectPublishedFileTree(t.Context(), request, original, acceptTestTree)
	require.NoError(t, err)
	record, err := proof.Record()
	require.NoError(t, err)
	moved := filepath.Join(base, "original-parent")
	require.NoError(t, os.Rename(parent, moved))
	_, err = productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	require.NoError(t, os.Rename(filepath.Join(moved, "canonical"), request.Destination))
	current, err := productfiles.ExistingDirectory(request.Destination)
	require.NoError(t, err)
	identity, err := current.Identity()
	require.NoError(t, err)
	require.Equal(t, original.DirectoryIdentity, identity)
	reopened, err := source.InspectRetainedFileTree(t.Context(), request, record, proof.Digest(), acceptTestTree)
	require.Error(t, err)
	require.Nil(t, reopened)
	require.FileExists(t, filepath.Join(request.Destination, "config.env"))
}

func TestCanonicalFileInspectionRejectsForgedRecord(t *testing.T) {
	source, request := restoreFileTreeFixture(t)
	original, err := source.PublishFileTree(t.Context(), request, acceptTestTree)
	require.NoError(t, err)
	proof, err := source.InspectPublishedFileTree(t.Context(), request, original, acceptTestTree)
	require.NoError(t, err)
	record, err := proof.Record()
	require.NoError(t, err)
	for _, kind := range []string{"digest", "identity", "parent", "backup", "unknown", "noncanonical", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			data := append([]byte(nil), record...)
			expected := proof.Digest()
			var decoded fileTreeInspectionRecord
			require.NoError(t, json.Unmarshal(record, &decoded))
			switch kind {
			case "digest":
				expected = strings.Repeat("b", 64)
			case "identity":
				decoded.Tree.DirectoryIdentity = "missing-original"
			case "parent":
				decoded.Tree.ParentIdentity = "missing-parent"
			case "backup":
				decoded.BackupSHA256 = strings.Repeat("c", 64)
			case "unknown":
				data = append([]byte(`{"unknown":true,`), record[1:]...)
			case "noncanonical":
				data = append([]byte(" "), record...)
			case "oversize":
				data = make([]byte, fileTreeInspectionMaxBytes+1)
			}
			if kind == "identity" || kind == "parent" || kind == "backup" {
				data, err = json.Marshal(decoded, json.Deterministic(true))
				require.NoError(t, err)
			}
			if kind != "digest" {
				expected = historySHA256(data)
			}
			reopened, err := source.InspectRetainedFileTree(t.Context(), request, data, expected, acceptTestTree)
			require.Error(t, err)
			require.Nil(t, reopened)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, proof.Check(ctx, source, request, acceptTestTree), context.Canceled)
	record[0] = 'x'
	fresh, err := proof.Record()
	require.NoError(t, err)
	require.NotEqual(t, record, fresh, "returned bytes must not change retained private evidence")
	var absent *VerifiedFileTree
	require.Error(t, absent.Check(t.Context(), source, request, acceptTestTree))
}
