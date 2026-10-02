package app

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starport/internal/audit"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configrevision"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/stretchr/testify/require"
)

// sourceKeyEdit is the field-save key of the catalog source API key.
const sourceKeyEdit = "catalog_source_api_key"

// sharedSourceDeployment is a shared deployment whose revision 1 selects the
// Starmap source without a credential. Each replica can start from it.
func sharedSourceDeployment(t *testing.T) sharedConfigurationDeployment {
	t.Helper()
	deployment := newSharedConfigurationDeployment(t)
	deployment.write(t, testSealer(t, deployment.environment["STARPORT_SECURITY_MASTER_KEY"]), "", map[string]string{
		catalogconfig.Source:    config.CatalogSourceStarmap,
		catalogconfig.SourceURL: "https://catalog.example/api/v1",
	})
	return deployment
}

func testSealer(t *testing.T, key string) configrevision.Sealer {
	t.Helper()
	sealer, err := credentials.NewEncryptionService([]byte(key))
	require.NoError(t, err)
	return sealer
}

// withEnvironment returns a deployment with changed process variables.
func (d sharedConfigurationDeployment) withEnvironment(changes map[string]string) sharedConfigurationDeployment {
	environment := maps.Clone(d.environment)
	maps.Copy(environment, changes)
	return sharedConfigurationDeployment{environment: environment}
}

func sourceKeySave(replica *configurationReplica, operationID, expected, key string) config.FieldSave {
	return config.FieldSave{
		OperationID: operationID, DeploymentID: replica.cfg.EffectivePaths().DeploymentID,
		ExpectedRevision: expected, Edits: map[string]*string{sourceKeyEdit: &key},
	}
}

func requireConfigurationRefusal(t *testing.T, err error, reason string) *config.Refusal {
	t.Helper()
	var refusal *config.Refusal
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, reason, refusal.Reason, refusal.Message)
	return refusal
}

func requireHead(t *testing.T, replica *configurationReplica, sequence int64) configrevision.Revision {
	t.Helper()
	current, err := replica.app.configuration.Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, sequence, current.Sequence)
	return current
}

func configurationAuditRecords(t *testing.T, replica *configurationReplica) []audit.Record {
	t.Helper()
	trail, err := audit.Open(replica.db, 0)
	require.NoError(t, err)
	page, err := trail.List(t.Context(), audit.Query{Limit: audit.MaxListLimit})
	require.NoError(t, err)
	return page.Records
}

func countAuditAction(records []audit.Record, action string) int {
	count := 0
	for _, record := range records {
		if record.Action == action {
			count++
		}
	}
	return count
}

func TestSharedConfigurationSaveRefusesStaleRevision(t *testing.T) {
	deployment := sharedSourceDeployment(t)
	replicaA, replicaB := deployment.mustStart(t), deployment.mustStart(t)

	receipt, err := replicaA.app.configurationOps.Save(t.Context(), sourceKeySave(replicaA, "op-first", "1", "first-source-key"), "operator:test")
	require.NoError(t, err)
	require.Equal(t, int64(2), receipt.Saved.Sequence)
	require.Equal(t, []string{sourceKeyEdit}, receipt.Settings)

	// Replica B read revision 1. Its save and its validation report both revisions.
	_, err = replicaB.app.configurationOps.Save(t.Context(), sourceKeySave(replicaB, "op-second", "1", "second-source-key"), "operator:test")
	refusal := requireConfigurationRefusal(t, err, config.RefusalStaleRevision)
	require.Equal(t, "1", refusal.Expected)
	require.Equal(t, "2", refusal.Current)
	validation, err := replicaB.app.configurationOps.Validate(t.Context(), sourceKeySave(replicaB, "", "1", "second-source-key"))
	require.NoError(t, err)
	require.False(t, validation.Valid)
	require.Equal(t, config.RefusalStaleRevision, validation.Refusal.Reason)
	require.Equal(t, int64(2), validation.Current.Sequence)
	_, found, err := replicaB.app.configurationOps.Receipt(t.Context(), "op-second")
	require.NoError(t, err)
	require.False(t, found, "a refused save writes no revision")
	requireHead(t, replicaA, 2)

	// Two replicas that save at the same head: one commits, one is stale.
	var wait sync.WaitGroup
	errs := make([]error, 2)
	for index, replica := range []*configurationReplica{replicaA, replicaB} {
		wait.Go(func() {
			_, errs[index] = replica.app.configurationOps.Save(context.Background(), sourceKeySave(replica, "op-race-"+string(rune('a'+index)), "2", "race-source-key"), "operator:test")
		})
	}
	wait.Wait()
	committed := 0
	for _, err := range errs {
		if err == nil {
			committed++
			continue
		}
		refusal := requireConfigurationRefusal(t, err, config.RefusalStaleRevision)
		require.Equal(t, "2", refusal.Expected)
		require.Equal(t, "3", refusal.Current)
	}
	require.Equal(t, 1, committed)
	requireHead(t, replicaA, 3)
}

func TestFailedActivationReportsSavedAndAppliedRevisions(t *testing.T) {
	deployment := sharedSourceDeployment(t)
	replicaA := deployment.mustStart(t)
	// Replica B has another master key. It starts from revision 1, which seals nothing.
	replicaB := deployment.withEnvironment(map[string]string{"STARPORT_SECURITY_MASTER_KEY": strings.Repeat("o", 32)}).mustStart(t)

	const secret = "activation-source-key"
	receipt, err := replicaA.app.configurationOps.Save(t.Context(), sourceKeySave(replicaA, "op-activation", "1", secret), "operator:test")
	require.NoError(t, err)
	require.Equal(t, config.OperationSaved, receipt.Status, "a running replica applies a new revision at restart")
	require.Equal(t, config.ManagementShared, receipt.Management)
	require.Equal(t, int64(2), receipt.Saved.Sequence)
	require.Equal(t, int64(1), receipt.Applied.Sequence)
	require.Empty(t, receipt.ActivationError)
	require.Equal(t, int64(2), replicaA.cfg.AppliedRevision().Desired, "the save observes the new head")

	// Replica B cannot open the sealed credential, so it reports why it cannot activate the revision.
	failed, found, err := replicaB.app.configurationOps.Receipt(t.Context(), "op-activation")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, config.OperationSaved, failed.Status)
	require.Equal(t, receipt.Saved, failed.Saved)
	require.Equal(t, int64(1), failed.Applied.Sequence)
	require.Contains(t, failed.ActivationError, sourceKeyEdit)
	require.NotContains(t, failed.ActivationError, secret)
	require.Equal(t, int64(1), replicaB.cfg.AppliedRevision().Applied)

	// A restart of replica A applies the saved revision.
	restarted := deployment.mustStart(t)
	applied, found, err := restarted.app.configurationOps.Receipt(t.Context(), "op-activation")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, config.OperationApplied, applied.Status)
	require.Equal(t, applied.Saved, applied.Applied)
	require.Equal(t, secret, restarted.cfg.Catalog.SourceAPIKey)
}

func TestConfigurationSaveExactRetryReturnsOriginalReceipt(t *testing.T) {
	deployment := sharedSourceDeployment(t)
	replicaA, replicaB := deployment.mustStart(t), deployment.mustStart(t)
	request := sourceKeySave(replicaA, "op-retry", "1", "retry-source-key")

	original, err := replicaA.app.configurationOps.Save(t.Context(), request, "operator:test")
	require.NoError(t, err)
	saves := countAuditAction(configurationAuditRecords(t, replicaA), config.ActionSave)
	require.Equal(t, 1, saves)

	// The same request from the same replica and from another replica returns the original receipt.
	for _, replica := range []*configurationReplica{replicaA, replicaB} {
		retried, err := replica.app.configurationOps.Save(t.Context(), request, "operator:other")
		require.NoError(t, err)
		require.Equal(t, original, retried)
	}
	stored, found, err := replicaA.app.configurationOps.Receipt(t.Context(), "op-retry")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, original, stored)

	// A retry after the head moved still returns the original receipt.
	_, err = replicaA.app.configurationOps.Save(t.Context(), sourceKeySave(replicaA, "op-next", "2", "next-source-key"), "operator:test")
	require.NoError(t, err)
	retried, err := replicaB.app.configurationOps.Save(t.Context(), request, "operator:test")
	require.NoError(t, err)
	require.Equal(t, original, retried)
	requireHead(t, replicaA, 3)
	require.Equal(t, saves+1, countAuditAction(configurationAuditRecords(t, replicaA), config.ActionSave), "a retry writes no audit record")
}

func TestConfigurationSaveRefusesReusedOperationID(t *testing.T) {
	deployment := sharedSourceDeployment(t)
	replica := deployment.mustStart(t)
	operations := replica.app.configurationOps
	_, err := operations.Save(t.Context(), sourceKeySave(replica, "op-reused", "1", "first-source-key"), "operator:test")
	require.NoError(t, err)

	initialization, found, err := replica.app.configuration.RevisionAt(t.Context(), 1)
	require.NoError(t, err)
	require.True(t, found)
	for name, request := range map[string]config.FieldSave{
		"other value":             sourceKeySave(replica, "op-reused", "1", "other-source-key"),
		"other expected revision": sourceKeySave(replica, "op-reused", "2", "first-source-key"),
		"other setting":           {OperationID: "op-reused", DeploymentID: replica.cfg.EffectivePaths().DeploymentID, ExpectedRevision: "1", Edits: map[string]*string{"catalog_source_token": new("first-source-key")}},
		"initialization ID":       sourceKeySave(replica, initialization.OperationID, "1", "first-source-key"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := operations.Save(t.Context(), request, "operator:test")
			requireConfigurationRefusal(t, err, config.RefusalOperationConflict)
			requireHead(t, replica, 2)
		})
	}

	// The operation ID keeps its original receipt.
	receipt, found, err := operations.Receipt(t.Context(), "op-reused")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(2), receipt.Saved.Sequence)
	require.Equal(t, 1, countAuditAction(configurationAuditRecords(t, replica), config.ActionSave))
}

func TestConfigurationSaveRefusesForeignDeployment(t *testing.T) {
	deployment := sharedSourceDeployment(t)
	replica := deployment.mustStart(t)
	operations := replica.app.configurationOps

	for name, deploymentID := range map[string]string{"other deployment": "other-deployment", "absent deployment": ""} {
		t.Run(name, func(t *testing.T) {
			request := sourceKeySave(replica, "op-foreign", "1", "foreign-source-key")
			request.DeploymentID = deploymentID
			_, err := operations.Save(t.Context(), request, "operator:test")
			requireConfigurationRefusal(t, err, config.RefusalForeignDeployment)
			_, err = operations.Validate(t.Context(), request)
			requireConfigurationRefusal(t, err, config.RefusalForeignDeployment)
			_, err = operations.TestConnection(t.Context(), request)
			requireConfigurationRefusal(t, err, config.RefusalForeignDeployment)
		})
	}
	requireHead(t, replica, 1)
	_, found, err := operations.Receipt(t.Context(), "op-foreign")
	require.NoError(t, err)
	require.False(t, found)
	require.Zero(t, countAuditAction(configurationAuditRecords(t, replica), config.ActionSave))
}

func TestExternalManagementRefusesConfigurationWrites(t *testing.T) {
	deployment := newSharedConfigurationDeployment(t).withEnvironment(map[string]string{"STARPORT_CONFIG_MANAGEMENT": config.ManagementExternal})
	replica := deployment.mustStart(t)
	operations := replica.app.configurationOps
	before, err := os.ReadFile(replica.file)
	require.NoError(t, err)
	request := config.FieldSave{
		OperationID: "op-external", DeploymentID: replica.cfg.EffectivePaths().DeploymentID,
		ExpectedRevision: replica.cfg.AppliedRevision().FileChecksum,
		Edits:            map[string]*string{"catalog_acquisition_interval": new("9m")},
	}

	_, err = operations.Save(t.Context(), request, "operator:test")
	requireConfigurationRefusal(t, err, config.RefusalExternalManagement)
	_, err = operations.Validate(t.Context(), request)
	requireConfigurationRefusal(t, err, config.RefusalExternalManagement)
	_, err = operations.TestConnection(t.Context(), request)
	requireConfigurationRefusal(t, err, config.RefusalExternalManagement)
	_, err = InitializeSharedConfiguration(t.Context(), replica.cfg, configrevision.Request{OperationID: "op-external-init"})
	requireConfigurationRefusal(t, err, config.RefusalExternalManagement)
	for _, target := range []string{configrevision.AuthorityShared, configrevision.AuthorityLocal} {
		_, err = MigrateConfiguration(t.Context(), replica.cfg, target, configrevision.Request{OperationID: "op-external-migrate"})
		requireConfigurationRefusal(t, err, config.RefusalExternalManagement)
	}

	// Nothing changed: the file, the journal, and the shared store.
	after, err := os.ReadFile(replica.file)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoFileExists(t, filepath.Join(filepath.Dir(replica.file), ".starport-config-operations.json"))
	store, err := openConfigurationStore(replica.cfg, replica.db)
	require.NoError(t, err)
	_, err = store.Head(t.Context())
	require.ErrorIs(t, err, configrevision.ErrNotInitialized)

	// Reads continue and report the external controller.
	require.NotEmpty(t, operations.Schema())
	effective := operations.Effective()
	require.Equal(t, config.ManagementExternal, effective.Management)
	require.Equal(t, config.ManagementExternal, effective.Controller)
	_, found, err := operations.Receipt(t.Context(), "op-external")
	require.NoError(t, err)
	require.False(t, found)
}

func TestConfigurationAuditRecordsOmitValues(t *testing.T) {
	t.Run("shared", func(t *testing.T) {
		deployment := sharedSourceDeployment(t)
		replica := deployment.mustStart(t)
		operations := replica.app.configurationOps
		secrets := []string{"audit-source-key-one", "audit-source-key-two"}
		for index, secret := range secrets {
			_, err := operations.Save(t.Context(), sourceKeySave(replica, "op-audit-"+string(rune('a'+index)), []string{"1", "2"}[index], secret), "operator:test")
			require.NoError(t, err)
		}
		// A refused save writes no audit record.
		_, err := operations.Save(t.Context(), sourceKeySave(replica, "op-audit-stale", "1", "refused-source-key"), "operator:test")
		requireConfigurationRefusal(t, err, config.RefusalStaleRevision)

		records := configurationAuditRecords(t, replica)
		var subjects []string
		for _, record := range records {
			if record.Action == config.ActionSave {
				subjects = append(subjects, record.Subject)
				require.Equal(t, "operator:test", record.Actor)
				require.Equal(t, audit.OutcomeOK, record.Outcome)
			}
		}
		var expected []string
		for _, sequence := range []int64{2, 3} {
			revision, found, err := replica.app.configuration.RevisionAt(t.Context(), sequence)
			require.NoError(t, err)
			require.True(t, found)
			expected = append(expected, revision.RevisionID+" settings="+sourceKeyEdit)
		}
		require.ElementsMatch(t, expected, subjects)
		requireNoAuditValue(t, records, append(secrets, "refused-source-key")...)
	})
	t.Run("local", func(t *testing.T) {
		deployment := newSharedConfigurationDeployment(t).withEnvironment(map[string]string{"STARPORT_CONFIG_MANAGEMENT": config.ManagementLocal})
		replica := deployment.mustStart(t)
		const secret = "audit-local-source-token"
		receipt, err := replica.app.configurationOps.Save(t.Context(), config.FieldSave{
			OperationID: "op-audit-local", DeploymentID: replica.cfg.EffectivePaths().DeploymentID,
			ExpectedRevision: replica.cfg.AppliedRevision().FileChecksum,
			Edits:            map[string]*string{"catalog_source_token": new(secret)},
		}, "operator:test")
		require.NoError(t, err)
		require.Empty(t, receipt.AuditError)

		records := configurationAuditRecords(t, replica)
		require.Equal(t, 1, countAuditAction(records, config.ActionSave))
		for _, record := range records {
			if record.Action == config.ActionSave {
				require.Equal(t, "local:"+receipt.Saved.Revision+" settings=catalog_source_token", record.Subject)
			}
		}
		requireNoAuditValue(t, records, secret)
	})
}

func requireNoAuditValue(t *testing.T, records []audit.Record, secrets ...string) {
	t.Helper()
	for _, record := range records {
		for _, secret := range secrets {
			for _, field := range []string{record.Actor, record.Action, record.Subject, record.Outcome, record.RequestID} {
				require.NotContains(t, field, secret)
			}
		}
	}
}

func TestFieldSaveRefusesManagementChange(t *testing.T) {
	shared := sharedSourceDeployment(t)
	local := newSharedConfigurationDeployment(t).withEnvironment(map[string]string{"STARPORT_CONFIG_MANAGEMENT": config.ManagementLocal})
	for name, replica := range map[string]*configurationReplica{"shared": shared.mustStart(t), "local": local.mustStart(t)} {
		t.Run(name, func(t *testing.T) {
			operations := replica.app.configurationOps
			expected := "1"
			if name == "local" {
				expected = replica.cfg.AppliedRevision().FileChecksum
			}
			before, err := os.ReadFile(replica.file)
			require.NoError(t, err)
			for _, key := range []string{"config_management", "STARPORT_CONFIG_MANAGEMENT"} {
				request := config.FieldSave{
					OperationID: "op-management", DeploymentID: replica.cfg.EffectivePaths().DeploymentID,
					ExpectedRevision: expected, Edits: map[string]*string{key: new(config.ManagementLocal)},
				}
				_, err := operations.Save(t.Context(), request, "operator:test")
				refusal := requireConfigurationRefusal(t, err, config.RefusalMigrationBoundary)
				require.Contains(t, refusal.Message, "starport config migrate")
				validation, err := operations.Validate(t.Context(), request)
				require.NoError(t, err)
				require.False(t, validation.Valid)
				require.Equal(t, config.RefusalMigrationBoundary, validation.Refusal.Reason)
			}
			after, err := os.ReadFile(replica.file)
			require.NoError(t, err)
			require.Equal(t, before, after)
			_, found, err := operations.Receipt(t.Context(), "op-management")
			require.NoError(t, err)
			require.False(t, found)
		})
	}

	// A shared field save also keeps the acquisition policy, which only config apply changes.
	replica := shared.mustStart(t)
	request := config.FieldSave{
		OperationID: "op-policy", DeploymentID: replica.cfg.EffectivePaths().DeploymentID,
		ExpectedRevision: "1", Edits: map[string]*string{"catalog_acquisition_interval": new("9m")},
	}
	_, err := replica.app.configurationOps.Save(t.Context(), request, "operator:test")
	refusal := requireConfigurationRefusal(t, err, config.RefusalPolicyChange)
	require.Contains(t, refusal.Message, "starport config apply")
	requireHead(t, replica, 1)

	schema := replica.app.configurationOps.Schema()
	management := schema[len(schema)-1]
	require.Equal(t, "config_management", management.Key)
	require.Equal(t, config.MutabilityMigrationOnly, management.Mutability)
}
