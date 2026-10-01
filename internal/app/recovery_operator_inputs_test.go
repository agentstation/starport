package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func operatorInputFixture(t *testing.T) (*config.Config, *sqlstore.DB, blob.RestoreTarget, RecoveryOperatorInputsRequest) {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "inputs")
	_, err := productfiles.CreateDirectory(parent)
	require.NoError(t, err)
	cfg, err := config.NewLoader().WithPaths(config.PathsForConfigDir(parent)).WithEnvironment(map[string]string{"STARPORT_SECURITY_MASTER_KEY": strings.Repeat("x", 32)}).Load(t.Context())
	require.NoError(t, err)
	for _, path := range []string{cfg.Storage.Badger.Path, filepath.Dir(cfg.Storage.SQL.SQLite.Path), cfg.Files.Path} {
		_, err = productfiles.NewDirectory(path)
		require.NoError(t, err)
	}
	kv, err := storage.Open(cfg.RuntimeStorage())
	require.NoError(t, err)
	require.NoError(t, kv.Close())
	db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(t.Context()))
	assets, err := blob.FilesystemRestoreTarget(cfg.Files.Path)
	require.NoError(t, err)
	token, err := localauth.NewStore(cfg.EffectivePaths().LocalTokenFile)
	require.NoError(t, err)
	_, err = token.Rotate(t.Context(), time.Now())
	require.NoError(t, err)
	require.NoError(t, cfg.InitializeInferencePolicy(t.Context(), false))
	journal := filepath.Join(parent, "journal")
	_, err = productfiles.CreateDirectory(journal)
	require.NoError(t, err)
	target, err := configuredRecoveryTarget(t.Context(), cfg, db, assets, "")
	require.NoError(t, err)
	return cfg, db, assets, RecoveryOperatorInputsRequest{Operation: recovery.RestoreOperation{ID: "recover-inputs", FencingEvidence: "incident/writers-stopped"}, JournalDirectory: journal, ExpectedTargetSHA256: target}
}

func TestRecoveryOperatorInputsRetainsExactOwnerSelection(t *testing.T) {
	cfg, db, assets, request := operatorInputFixture(t)
	token, err := os.ReadFile(cfg.EffectivePaths().LocalTokenFile)
	require.NoError(t, err)
	first, err := VerifyRecoveryOperatorInputs(t.Context(), cfg, db, assets, request)
	require.NoError(t, err)
	require.True(t, first.Report().Restricted)
	require.Equal(t, 2, first.Report().SelectedFiles)
	require.Len(t, first.Report().DecisionSHA256, 64)
	repeated, err := VerifyRecoveryOperatorInputs(t.Context(), cfg, db, assets, request)
	require.NoError(t, err)
	require.Equal(t, first.Report(), repeated.Report())
	require.NoError(t, first.Check(t.Context(), cfg, db, assets, request))
	retained, err := os.ReadFile(filepath.Join(request.JournalDirectory, recoveryOperatorInputsFile))
	require.NoError(t, err)
	require.Contains(t, string(retained), "inference-credential-policy/policy.json")
	require.NotContains(t, string(retained), cfg.Security.MasterKey)
	require.NotContains(t, string(retained), "starport_local_")
	require.Equal(t, "<private recovery operator inputs>", fmt.Sprintf("%#v", first))
	require.NotContains(t, fmt.Sprintf("%p", first), "starport_local_")
	after, err := os.ReadFile(cfg.EffectivePaths().LocalTokenFile)
	require.NoError(t, err)
	require.Equal(t, token, after)
	require.NoError(t, db.PingContext(t.Context()))
}

func TestRecoveryOperatorInputsRefusesChangedSelections(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *config.Config, *sqlstore.DB, blob.RestoreTarget, *RecoveryOperatorInputsRequest)
	}{
		{"settings", func(_ *testing.T, c *config.Config, _ *sqlstore.DB, _ blob.RestoreTarget, _ *RecoveryOperatorInputsRequest) {
			c.Server.Port++
		}},
		{"encryption", func(_ *testing.T, c *config.Config, _ *sqlstore.DB, _ blob.RestoreTarget, _ *RecoveryOperatorInputsRequest) {
			c.Security.MasterKey = strings.Repeat("y", 32)
		}},
		{"token rotation", func(t *testing.T, c *config.Config, _ *sqlstore.DB, _ blob.RestoreTarget, _ *RecoveryOperatorInputsRequest) {
			s, e := localauth.NewStore(c.EffectivePaths().LocalTokenFile)
			require.NoError(t, e)
			_, e = s.Rotate(t.Context(), time.Now())
			require.NoError(t, e)
		}},
		{"same bytes replacement", func(t *testing.T, c *config.Config, _ *sqlstore.DB, _ blob.RestoreTarget, _ *RecoveryOperatorInputsRequest) {
			path := c.EffectivePaths().LocalTokenFile
			b, e := os.ReadFile(path)
			require.NoError(t, e)
			require.NoError(t, os.Rename(path, path+".old"))
			require.NoError(t, os.WriteFile(path, b, 0600))
		}},
		{"native target", func(t *testing.T, c *config.Config, _ *sqlstore.DB, _ blob.RestoreTarget, _ *RecoveryOperatorInputsRequest) {
			c.Storage.Badger.Path = filepath.Join(c.EffectivePaths().DataDir, "replacement")
			_, e := productfiles.CreateDirectory(c.Storage.Badger.Path)
			require.NoError(t, e)
		}},
		{"operation", func(_ *testing.T, _ *config.Config, _ *sqlstore.DB, _ blob.RestoreTarget, r *RecoveryOperatorInputsRequest) {
			r.Operation.ID = "other"
		}},
		{"inference policy", func(t *testing.T, c *config.Config, _ *sqlstore.DB, _ blob.RestoreTarget, _ *RecoveryOperatorInputsRequest) {
			require.NoError(t, os.WriteFile(filepath.Join(c.InferenceCredentialPolicyDirectory(), "policy.json"), []byte("invalid selected policy"), 0600))
		}},
		{"token canonical path", func(_ *testing.T, c *config.Config, _ *sqlstore.DB, _ blob.RestoreTarget, _ *RecoveryOperatorInputsRequest) {
			c.Security.LocalTokenPath += ".other"
		}},
		{"journal replacement", func(t *testing.T, _ *config.Config, _ *sqlstore.DB, _ blob.RestoreTarget, r *RecoveryOperatorInputsRequest) {
			path := filepath.Join(r.JournalDirectory, recoveryOperatorInputsFile)
			body, e := os.ReadFile(path)
			require.NoError(t, e)
			require.NoError(t, os.Rename(r.JournalDirectory, r.JournalDirectory+"-old"))
			_, e = productfiles.CreateDirectory(r.JournalDirectory)
			require.NoError(t, e)
			require.NoError(t, os.WriteFile(path, body, 0600))
		}},
		{"journal deletion", func(t *testing.T, _ *config.Config, _ *sqlstore.DB, _ blob.RestoreTarget, r *RecoveryOperatorInputsRequest) {
			require.NoError(t, os.Remove(filepath.Join(r.JournalDirectory, recoveryOperatorInputsFile)))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, db, assets, request := operatorInputFixture(t)
			first, err := VerifyRecoveryOperatorInputs(t.Context(), cfg, db, assets, request)
			require.NoError(t, err)
			body, err := os.ReadFile(filepath.Join(request.JournalDirectory, recoveryOperatorInputsFile))
			require.NoError(t, err)
			tc.change(t, cfg, db, assets, &request)
			require.Error(t, first.Check(t.Context(), cfg, db, assets, request))
			if tc.name != "journal deletion" {
				_, err = VerifyRecoveryOperatorInputs(t.Context(), cfg, db, assets, request)
				require.Error(t, err)
				after, e := os.ReadFile(filepath.Join(request.JournalDirectory, recoveryOperatorInputsFile))
				require.NoError(t, e)
				require.Equal(t, body, after)
			}
		})
	}
}

func TestRecoveryOperatorInputsRefusesBeforeRetainingDecision(t *testing.T) {
	cfg, db, assets, request := operatorInputFixture(t)
	wrong := request
	wrong.ExpectedTargetSHA256 = strings.Repeat("0", 64)
	_, err := VerifyRecoveryOperatorInputs(t.Context(), cfg, db, assets, wrong)
	require.ErrorIs(t, err, recovery.ErrConflict)
	wrong = request
	wrong.Operation.FencingEvidence = ""
	_, err = VerifyRecoveryOperatorInputs(t.Context(), cfg, db, assets, wrong)
	require.Error(t, err)
	_, err = VerifyRecoveryOperatorInputs(nil, cfg, db, assets, request)
	require.Error(t, err)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = VerifyRecoveryOperatorInputs(cancelled, cfg, db, assets, request)
	require.ErrorIs(t, err, context.Canceled)
	require.NoFileExists(t, filepath.Join(request.JournalDirectory, recoveryOperatorInputsFile))
	cfg.Server.Host = "0.0.0.0"
	store, err := localauth.NewStore(cfg.EffectivePaths().LocalTokenFile)
	require.NoError(t, err)
	original, err := store.Peek(t.Context())
	require.NoError(t, err)
	body, err := os.ReadFile(store.Path())
	require.NoError(t, err)
	require.True(t, original.Rotated())
	// A corrupt selected token never triggers first-boot creation or rotation.
	require.NoError(t, os.WriteFile(store.Path(), []byte("invalid token"), 0600))
	_, err = VerifyRecoveryOperatorInputs(t.Context(), cfg, db, assets, request)
	require.Error(t, err)
	after, err := os.ReadFile(store.Path())
	require.NoError(t, err)
	require.True(t, bytes.Equal(after, []byte("invalid token")))
	require.NoFileExists(t, filepath.Join(request.JournalDirectory, recoveryOperatorInputsFile))
	require.NoError(t, os.WriteFile(store.Path(), body, 0600))
}

func TestRecoveryOperatorInputsSharedNativeTargets(t *testing.T) {
	for _, mode := range []string{"postgres", "mysql"} {
		t.Run(mode, func(t *testing.T) {
			valkey, postgres, mysql := os.Getenv("TEST_VALKEY_URL"), os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_MYSQL_DSN")
			if valkey == "" || mode == "postgres" && postgres == "" || mode == "mysql" && mysql == "" {
				t.Skip("UNVERIFIED: native shared KV and SQL fixtures are required")
			}
			cfg, _, assets, request := operatorInputFixture(t)
			if mode == "postgres" {
				cfg.Storage.Mode = "valkey"
				cfg.Storage.Valkey.URL = valkey
				cfg.Files.Backend = config.BlobBackendObjectStore
				cfg.Files.ObjectStore = config.ObjectStoreConfig{Bucket: "operator-inputs-native-scope", Region: "us-east-1", Endpoint: os.Getenv("TEST_BLOB_S3_ENDPOINT"), AccessKeyID: "starport-test", SecretAccessKey: "starport-local-test-only"}
				var err error
				assets, err = restoreBlobTarget(t.Context(), cfg.Files)
				require.NoError(t, err)
			}
			cfg.Storage.SQL.Mode = mode
			cfg.Storage.SQL.Postgres.URL = postgres
			cfg.Storage.SQL.MySQL.DSN = mysql
			db, err := sqlstore.Open(cfg.Storage.RuntimeSQL())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			require.NoError(t, db.PingContext(t.Context()))
			if mode == "postgres" {
				store, err := storage.OpenValkey(cfg.RuntimeStorage().Valkey)
				require.NoError(t, err)
				request.ValkeyIncarnation, err = store.(storage.IncarnationProvider).ObserveIncarnation(t.Context())
				require.NoError(t, err)
				require.NoError(t, store.Close())
			}
			request.ExpectedTargetSHA256, err = configuredRecoveryTarget(t.Context(), cfg, db, assets, request.ValkeyIncarnation)
			require.NoError(t, err)
			first, err := VerifyRecoveryOperatorInputs(t.Context(), cfg, db, assets, request)
			require.NoError(t, err)
			require.NoError(t, first.Check(t.Context(), cfg, db, assets, request))
			if mode == "postgres" {
				cfg.Storage.Valkey.Password = "changed-selection"
			} else {
				cfg.Storage.SQL.MySQL.DSN += "&timeout=2s"
			}
			require.Error(t, first.Check(t.Context(), cfg, db, assets, request))
			require.True(t, first.Report().Restricted)
		})
	}
}

func TestRecoveryOperatorInputsRefusesJournalInsideCanonicalState(t *testing.T) {
	for _, role := range []string{"badger", "policy", "primary"} {
		t.Run(role, func(t *testing.T) {
			cfg, db, assets, request := operatorInputFixture(t)
			switch role {
			case "badger":
				request.JournalDirectory = cfg.Storage.Badger.Path
			case "policy":
				request.JournalDirectory = cfg.InferenceCredentialPolicyDirectory()
			case "primary":
				paths := cfg.EffectivePaths()
				paths.ConfigFile = filepath.Join(request.JournalDirectory, recoveryOperatorInputsFile)
				selected, err := config.NewLoader().WithPaths(paths).WithEnvironment(map[string]string{"STARPORT_SECURITY_MASTER_KEY": cfg.Security.MasterKey}).Load(t.Context())
				require.NoError(t, err)
				cfg = selected
			}
			_, err := VerifyRecoveryOperatorInputs(t.Context(), cfg, db, assets, request)
			require.Error(t, err)
			require.NoFileExists(t, filepath.Join(request.JournalDirectory, recoveryOperatorInputsFile))
		})
	}
}

func TestRecoveryOperatorInputsRechecksAfterConfigurationReload(t *testing.T) {
	cfg, db, assets, request := operatorInputFixture(t)
	first, err := VerifyRecoveryOperatorInputs(t.Context(), cfg, db, assets, request)
	require.NoError(t, err)
	reloaded, err := config.NewLoader().WithPaths(cfg.EffectivePaths()).WithEnvironment(map[string]string{"STARPORT_SECURITY_MASTER_KEY": cfg.Security.MasterKey}).Load(t.Context())
	require.NoError(t, err)
	repeated, err := VerifyRecoveryOperatorInputs(t.Context(), reloaded, db, assets, request)
	require.NoError(t, err)
	require.Equal(t, first.Report(), repeated.Report())
	require.NoError(t, first.Check(t.Context(), reloaded, db, assets, request))
}
