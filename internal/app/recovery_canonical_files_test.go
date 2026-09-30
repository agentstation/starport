package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/stretchr/testify/require"
)

func canonicalSelectedInputs(t *testing.T, cfg *config.Config) {
	t.Helper()
	token, err := localauth.NewStore(cfg.EffectivePaths().LocalTokenFile)
	require.NoError(t, err)
	_, err = token.Rotate(t.Context(), time.Now())
	require.NoError(t, err)
	require.NoError(t, cfg.InitializeInferencePolicy(t.Context(), false))
}

func TestCanonicalCurrentSourceSelectionBindsNativeFile(t *testing.T) {
	cfg, _, _, _ := operatorInputFixture(t)
	cfg.Catalog.Source = config.CatalogSourceFile
	cfg.Catalog.SourceURL = filepath.Join(cfg.EffectivePaths().ConfigDir, "selected-source.json")
	require.NoError(t, os.WriteFile(cfg.Catalog.SourceURL, []byte("selected source"), 0600))
	first, err := cfg.InspectRecoverySelection(t.Context())
	require.NoError(t, err)
	require.Contains(t, string(first.PrivateEvidence()), "source-file")
	require.NoError(t, os.Rename(cfg.Catalog.SourceURL, cfg.Catalog.SourceURL+"-old"))
	require.NoError(t, os.WriteFile(cfg.Catalog.SourceURL, []byte("selected source"), 0600))
	second, err := cfg.InspectRecoverySelection(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, first.PrivateEvidence(), second.PrivateEvidence())
}

func canonicalRoleFixture(t *testing.T, role string) (*config.Config, recovery.PublishFilesRequest, recovery.PublishFilesResult) {
	t.Helper()
	var cfg *config.Config
	var request recovery.PublishFilesRequest
	switch role {
	case config.BaselineRole:
		cfg, request, _ = baselinePublicationFixture(t, "journal")
	case config.RuntimeEvidenceRole:
		cfg, request, _ = runtimePublicationFixture(t, "completed-migration")
	default:
		cfg, request = policyPublicationRoleStagingFixture(t, role, false, "native")
	}
	result, err := PublishBackupFiles(t.Context(), cfg, request)
	require.NoError(t, err)
	canonicalSelectedInputs(t, cfg)
	return cfg, request, result
}

func TestRecoveryCanonicalFilesChecksRoleOwnersAndInactiveEvidence(t *testing.T) {
	for _, role := range canonicalRecoveryRoles {
		t.Run(role, func(t *testing.T) {
			cfg, request, publication := canonicalRoleFixture(t, role)
			originals := canonicalAllOriginals(t, cfg, request, publication)
			proof, err := VerifyRecoveryCanonicalFiles(t.Context(), cfg, request.PrepareRequest, originals)
			require.NoError(t, err)
			body, err := proof.Record()
			require.NoError(t, err)
			require.Contains(t, string(body), "restored-owner-state")
			require.Contains(t, string(body), "current-target")
			require.NotContains(t, string(body), cfg.Security.MasterKey)
			require.NotContains(t, string(body), "private local token")
			require.Equal(t, "<private verified canonical files>", fmt.Sprintf("%#v", proof))
			var record canonicalFilesRecord
			require.NoError(t, json.Unmarshal(body, &record))
			require.Len(t, record.Publications, len(originals))
			require.True(t, slices.ContainsFunc(record.Publications, func(record canonicalPublicationRecord) bool { return record.Role == role }))
			if role != config.RuntimeEvidenceRole {
				require.Contains(t, string(body), "verified-staging")
			}
			if role == config.RuntimeEvidenceRole || role == config.BaselineRole {
				require.Contains(t, string(body), "verified-history")
			}
			restarted, err := InspectRecoveryCanonicalFiles(t.Context(), cfg, request.PrepareRequest, body, proof.Digest())
			require.NoError(t, err)
			again, err := restarted.Record()
			require.NoError(t, err)
			require.Equal(t, body, again)
			require.NoError(t, restarted.Check(t.Context(), cfg, request.PrepareRequest))
			before := bytes.Clone(body)
			body[0] = '['
			retained, err := proof.Record()
			require.NoError(t, err)
			require.Equal(t, before, retained)
			requirePublicationClosedSQLAndKV(t, cfg)
		})
	}
}

func requirePublicationClosedSQLAndKV(t *testing.T, cfg *config.Config) {
	t.Helper()
	_, err := storage.Open(cfg.RuntimeStorage())
	require.ErrorIs(t, err, storage.ErrImportRestricted)
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	require.ErrorIs(t, db.CheckImportBarrier(t.Context()), sqlstore.ErrImportRestricted)
	require.NoError(t, db.Close())
}

func TestRecoveryCanonicalFilesRefusesDiagnosticOnlyOrSubstitutedOriginals(t *testing.T) {
	cfg, request, publication := canonicalRoleFixture(t, config.InferenceCredentialPolicyRole)
	for _, mode := range []string{"missing", "duplicate", "role", "parent", "tree", "digest", "outcome", "operation", "manifest", "fence", "staging"} {
		t.Run(mode, func(t *testing.T) {
			changed := publication
			switch mode {
			case "role":
				changed.Role = config.BaselineRole
			case "parent":
				changed.Tree.ParentIdentity = ""
			case "tree":
				changed.Tree.DirectoryIdentity = ""
			case "digest":
				changed.Tree.SelectionSHA256 = strings.Repeat("a", 64)
			case "outcome":
				changed.Tree.Published, changed.Tree.Reused = false, false
			case "operation":
				changed.Preparation.Prepared.OperationID += "-other"
			case "manifest":
				changed.Preparation.Prepared.ManifestSHA256 = strings.Repeat("b", 64)
			case "fence":
				changed.Preparation.Prepared.FencingEvidence = "another fence"
			case "staging":
				changed.Preparation.FilesDirectory += "-other"
			}
			originals := []recovery.PublishFilesResult{changed}
			if mode == "missing" {
				originals = nil
			}
			if mode == "duplicate" {
				originals = append(originals, changed)
			}
			_, err := VerifyRecoveryCanonicalFiles(t.Context(), cfg, request.PrepareRequest, originals)
			require.Error(t, err)
		})
	}
	var empty VerifiedCanonicalFiles
	_, err := empty.Record()
	require.ErrorIs(t, err, recovery.ErrConflict)
	require.Empty(t, empty.Digest())
	require.ErrorIs(t, empty.Check(t.Context(), cfg, request.PrepareRequest), recovery.ErrConflict)
}

func TestRecoveryCanonicalFilesPassiveRestartRefusesChangedEvidenceAndTargets(t *testing.T) {
	for _, mode := range []string{"missing-record", "corrupt-record", "wrong-seal", "directory", "parent", "missing-tree", "bytes", "settings", "token", "operation", "backup", "unresolved-role", "role-selection"} {
		t.Run(mode, func(t *testing.T) {
			cfg, request, publication := canonicalRoleFixture(t, config.InferenceCredentialPolicyRole)
			proof, err := VerifyRecoveryCanonicalFiles(t.Context(), cfg, request.PrepareRequest, []recovery.PublishFilesResult{publication})
			require.NoError(t, err)
			body, err := proof.Record()
			require.NoError(t, err)
			seal := proof.Digest()
			switch mode {
			case "missing-record":
				body = nil
			case "corrupt-record":
				body[0] = '['
			case "wrong-seal":
				seal = strings.Repeat("a", 64)
			case "directory", "parent":
				destination := publication.Tree.Destination
				if mode == "parent" {
					destination = filepath.Dir(destination)
				}
				require.NoError(t, os.Rename(destination, destination+"-old"))
				require.NoError(t, os.CopyFS(destination, os.DirFS(destination+"-old")))
				require.NoError(t, filepath.WalkDir(destination, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					mode := os.FileMode(0600)
					if entry.IsDir() {
						mode = 0700
					}
					return os.Chmod(path, mode)
				}))
			case "missing-tree":
				require.NoError(t, os.Rename(publication.Tree.Destination, publication.Tree.Destination+"-old"))
			case "bytes":
				require.NoError(t, os.WriteFile(filepath.Join(publication.Tree.Destination, "policy.json"), []byte("changed owner record"), 0600))
			case "settings":
				cfg.Server.Port++
			case "token":
				require.NoError(t, os.WriteFile(cfg.EffectivePaths().LocalTokenFile, []byte("invalid selected administrator token"), 0600))
			case "operation":
				request.Operation.ID += "-other"
			case "backup":
				request.ManifestSHA256 = strings.Repeat("b", 64)
			case "unresolved-role", "role-selection":
				var record canonicalFilesRecord
				require.NoError(t, json.Unmarshal(body, &record))
				if mode == "unresolved-role" {
					record.Files[0].Action = "remain_restricted"
				} else {
					record.Publications[0].Role = config.AcquisitionPolicyRole
				}
				body, err = json.Marshal(record, json.Deterministic(true))
				require.NoError(t, err)
				digest := sha256.Sum256(body)
				seal = hex.EncodeToString(digest[:])
			}
			before := canonicalTreeSnapshot(t, cfg.EffectivePaths().ConfigDir)
			_, err = InspectRecoveryCanonicalFiles(t.Context(), cfg, request.PrepareRequest, body, seal)
			require.Error(t, err)
			require.Equal(t, before, canonicalTreeSnapshot(t, cfg.EffectivePaths().ConfigDir), "passive refusal changed canonical state")
		})
	}
}

func canonicalTreeSnapshot(t *testing.T, directory string) map[string]string {
	t.Helper()
	result := map[string]string{}
	require.NoError(t, filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		stat, err := entry.Info()
		if err != nil {
			return err
		}
		result[path] = fmt.Sprintf("%s/%d/%d", stat.Mode(), stat.Size(), stat.ModTime().UnixNano())
		if stat.Mode().IsRegular() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(body)
			result[path] += "/" + hex.EncodeToString(digest[:])
		}
		return nil
	}))
	return result
}

func TestRecoveryCanonicalFilesFreshProcessAfterBlobRelease(t *testing.T) {
	if os.Getenv("STARPORT_CANONICAL_INSPECTION_CHILD") == "1" {
		cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(os.Getenv("STARPORT_CANONICAL_CONFIG_DIR"))).WithEnvFiles().WithEnvironment(map[string]string{
			"STARPORT_SECURITY_MASTER_KEY": os.Getenv("STARPORT_CANONICAL_MASTER"), "STARPORT_DEPLOYMENT_ID": os.Getenv("STARPORT_CANONICAL_DEPLOYMENT"), "STARPORT_INSTANCE_ID": os.Getenv("STARPORT_CANONICAL_INSTANCE"),
		}).Load(t.Context())
		require.NoError(t, err)
		body, err := os.ReadFile(os.Getenv("STARPORT_CANONICAL_RECORD"))
		require.NoError(t, err)
		var record canonicalFilesRecord
		require.NoError(t, json.Unmarshal(body, &record))
		digest := sha256.Sum256(body)
		proof, err := InspectRecoveryCanonicalFiles(t.Context(), cfg, record.Request, body, hex.EncodeToString(digest[:]))
		require.NoError(t, err)
		again, err := proof.Record()
		require.NoError(t, err)
		require.Equal(t, body, again)
		return
	}
	cfg, request, publication := canonicalRoleFixture(t, config.InferenceCredentialPolicyRole)
	proof, err := VerifyRecoveryCanonicalFiles(t.Context(), cfg, request.PrepareRequest, []recovery.PublishFilesResult{publication})
	require.NoError(t, err)
	body, err := proof.Record()
	require.NoError(t, err)
	source, _, _, err := inspectBackupRestore(t.Context(), cfg, request.PrepareRequest)
	require.NoError(t, err)
	identity, err := source.ImportIdentity(request.Operation)
	require.NoError(t, err)
	target, err := restoreBlobTarget(t.Context(), cfg.Files)
	require.NoError(t, err)
	require.NoError(t, target.(blob.ImportReplayActivator).ActivateImportAt(t.Context(), identity.ComponentOperation, identity.BlobOriginal, blob.ImportReplayPosition{}, proof.Digest()))
	// A new process can inspect the original record after native release.
	// Repeating preparation would conflict with the completed native receipt.
	recordPath := filepath.Join(t.TempDir(), "canonical-record.json")
	require.NoError(t, os.WriteFile(recordPath, body, 0600))
	before := canonicalTreeSnapshot(t, cfg.EffectivePaths().ConfigDir)
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRecoveryCanonicalFilesFreshProcessAfterBlobRelease$", "-test.v")
	paths := cfg.EffectivePaths()
	command.Env = append(os.Environ(), "STARPORT_CANONICAL_INSPECTION_CHILD=1", "STARPORT_CANONICAL_CONFIG_DIR="+paths.ConfigDir, "STARPORT_CANONICAL_MASTER="+cfg.Security.MasterKey, "STARPORT_CANONICAL_DEPLOYMENT="+paths.DeploymentID, "STARPORT_CANONICAL_INSTANCE="+paths.InstanceID, "STARPORT_CANONICAL_RECORD="+recordPath)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Equal(t, before, canonicalTreeSnapshot(t, cfg.EffectivePaths().ConfigDir))
}

func canonicalAllOriginals(t *testing.T, cfg *config.Config, request recovery.PublishFilesRequest, first recovery.PublishFilesResult) []recovery.PublishFilesResult {
	t.Helper()
	source, plan, _, err := inspectBackupRestore(t.Context(), cfg, request.PrepareRequest)
	require.NoError(t, err)
	selected, _, _, err := canonicalRecoverySelectionMode(t.Context(), cfg, source, plan, false)
	require.NoError(t, err)
	originals := []recovery.PublishFilesResult{first}
	for _, selection := range selected {
		if selection.role == first.Role {
			continue
		}
		request.Role = selection.role
		publication, err := PublishBackupFiles(t.Context(), cfg, request)
		require.NoError(t, err)
		originals = append(originals, publication)
	}
	return originals
}

func TestRecoveryCanonicalFilesRefusesUnresolvedSelectedRoleWithoutPreparation(t *testing.T) {
	source, capture := backupApplicationFixture(t)
	source.Catalog.WorkspacePath = filepath.Join(source.EffectivePaths().ConfigDir, "workspace")
	require.NoError(t, os.Mkdir(source.Catalog.WorkspacePath, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(source.Catalog.WorkspacePath, "providers.yaml"), []byte("operator workspace"), 0600))
	_, err := CloseBackupBoundary(t.Context(), source)
	require.NoError(t, err)
	receipt, err := CaptureBackup(t.Context(), source, capture)
	require.NoError(t, err)
	targetDirectory := filepath.Join(t.TempDir(), "target")
	_, err = productfiles.CreateDirectory(targetDirectory)
	require.NoError(t, err)
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(targetDirectory)).WithEnvFiles().WithEnvironment(map[string]string{"STARPORT_SECURITY_MASTER_KEY": source.Security.MasterKey, "STARPORT_DEPLOYMENT_ID": source.EffectivePaths().DeploymentID, "STARPORT_CATALOG_WORKSPACE_PATH": filepath.Join(targetDirectory, "workspace")}).Load(t.Context())
	require.NoError(t, err)
	canonicalSelectedInputs(t, cfg)
	request := recovery.PrepareRequest{VerifyRequest: recovery.VerifyRequest{Directory: receipt.Directory, ManifestSHA256: receipt.ManifestSHA256}, Operation: recovery.RestoreOperation{ID: "unresolved-owner", FencingEvidence: "writers stopped"}, FilesDirectory: filepath.Join(targetDirectory, "prepared")}
	before := canonicalTreeSnapshot(t, targetDirectory)
	_, err = VerifyRecoveryCanonicalFiles(t.Context(), cfg, request, nil)
	require.ErrorContains(t, err, "completed role owner disposition")
	require.Equal(t, before, canonicalTreeSnapshot(t, targetDirectory))
	require.NoDirExists(t, cfg.Storage.Badger.Path)
	require.NoFileExists(t, cfg.Storage.SQL.SQLite.Path)
	require.NoDirExists(t, request.FilesDirectory)
}

func TestCanonicalCurrentSourceUsesCatalogByteBound(t *testing.T) {
	cfg, _, _, _ := operatorInputFixture(t)
	cfg.Catalog.Source = config.CatalogSourceFile
	cfg.Catalog.SourceURL = filepath.Join(cfg.EffectivePaths().ConfigDir, "large-source.json")
	require.NoError(t, os.WriteFile(cfg.Catalog.SourceURL, bytes.Repeat([]byte("x"), (1<<20)+1), 0600))
	selection, err := cfg.InspectRecoverySelection(t.Context())
	require.NoError(t, err)
	require.Contains(t, string(selection.PrivateEvidence()), "source-file")
}

func TestRecoveryCanonicalFilesRefusesBoundedPreflight(t *testing.T) {
	var request recovery.PrepareRequest
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := InspectRecoveryCanonicalFiles(ctx, &config.Config{}, request, []byte("{}"), strings.Repeat("a", 64))
	require.ErrorIs(t, err, context.Canceled)
	_, err = InspectRecoveryCanonicalFiles(nil, &config.Config{}, request, []byte("{}"), strings.Repeat("a", 64))
	require.ErrorIs(t, err, recovery.ErrConflict)
	_, err = InspectRecoveryCanonicalFiles(t.Context(), nil, request, []byte("{}"), strings.Repeat("a", 64))
	require.ErrorIs(t, err, recovery.ErrConflict)
	_, err = InspectRecoveryCanonicalFiles(t.Context(), &config.Config{}, request, make([]byte, canonicalRecoveryMaxBytes+1), strings.Repeat("a", 64))
	require.ErrorIs(t, err, recovery.ErrConflict)
}
