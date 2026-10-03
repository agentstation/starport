package configrevision_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/audit"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configrevision"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/sqlstore"
)

func TestInitializeRaceCreatesOneHead(t *testing.T) {
	selected := postgresConfig(t)
	first, second := openDatabase(t, selected), openDatabase(t, selected)
	firstStore, _ := openStore(t, first, nil)
	secondStore, _ := openStore(t, second, nil)

	seeds := []map[string]string{
		{catalogconfig.AcquisitionInterval: "5m"},
		{catalogconfig.AcquisitionInterval: "9m"},
	}
	stores := []*configrevision.Store{firstStore, secondStore}
	results := make([]configrevision.Revision, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := range stores {
		wg.Go(func() {
			<-start
			results[index], errs[index] = stores[index].Initialize(context.Background(), seeds[index], "operator:test", "initialize-"+string(rune('a'+index)))
		})
	}
	close(start)
	wg.Wait()

	winner, loser := 0, 1
	if errs[0] != nil {
		winner, loser = 1, 0
	}
	require.NoError(t, errs[winner])
	var initialized *configrevision.InitializedError
	require.ErrorAs(t, errs[loser], &initialized)
	require.Equal(t, results[winner].Head, initialized.Head, "the loser reports the winner")
	require.Equal(t, int64(1), initialized.Head.Sequence)
	require.Equal(t, testNamespace, initialized.Head.Namespace)
	require.Contains(t, errs[loser].Error(), testNamespace)

	heads, revisions, records := countRows(t, first)
	require.Equal(t, [3]int{1, 1, 1}, [3]int{heads, revisions, records})
	page, err := audit.Open(first, 0)
	require.NoError(t, err)
	trail, err := page.List(t.Context(), audit.Query{})
	require.NoError(t, err)
	require.Equal(t, configrevision.ActionInitialize, trail.Records[0].Action)
	require.Equal(t, results[winner].RevisionID, trail.Records[0].Subject)

	current, err := secondStore.Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, seeds[winner], current.Values)
}

func TestUnavailableStoreIsNotEmpty(t *testing.T) {
	for name, selected := range backendConfigs(t) {
		t.Run(name, func(t *testing.T) {
			db := openDatabase(t, selected)
			witness := openDatabase(t, selected)
			store, _ := openStore(t, db, nil)
			_, err := store.Head(t.Context())
			require.ErrorIs(t, err, configrevision.ErrNotInitialized, "a reachable empty store is not initialized")

			require.NoError(t, db.Close())
			var unavailable *configrevision.UnavailableError
			_, err = store.Head(t.Context())
			require.ErrorAs(t, err, &unavailable)
			require.NotErrorIs(t, err, configrevision.ErrNotInitialized)
			_, err = store.Current(t.Context())
			require.ErrorAs(t, err, &unavailable)
			_, err = store.Preview(t.Context(), map[string]string{catalogconfig.AcquisitionInterval: "5m"})
			require.ErrorAs(t, err, &unavailable)
			_, err = store.Initialize(t.Context(), map[string]string{catalogconfig.AcquisitionInterval: "5m"}, "operator:test", "initialize-outage")
			require.ErrorAs(t, err, &unavailable)
			_, err = store.Migrate(t.Context(), configrevision.AuthorityShared, map[string]string{}, "operator:test", "migrate-outage")
			require.ErrorAs(t, err, &unavailable)
			_, err = store.Commit(t.Context(), 1, map[string]string{}, "operator:test", "commit-outage")
			require.ErrorAs(t, err, &unavailable)

			// An expired deadline is also unavailable, not empty.
			witnessStore, _ := openStore(t, witness, nil)
			expired, cancel := context.WithCancel(t.Context())
			cancel()
			_, err = witnessStore.Head(expired)
			require.ErrorAs(t, err, &unavailable)
			require.ErrorIs(t, err, context.Canceled)

			heads, revisions, records := countRows(t, witness)
			require.Equal(t, [3]int{0, 0, 0}, [3]int{heads, revisions, records}, "nothing is written")
		})
	}
}

func TestMigrateLocalToSharedRecordsRevisionAndAudit(t *testing.T) {
	for name, selected := range backendConfigs(t) {
		t.Run(name, func(t *testing.T) {
			db := openDatabase(t, selected)
			sealer, err := credentials.NewEncryptionService([]byte(strings.Repeat("k", 32)))
			require.NoError(t, err)
			store, trail := openStore(t, db, sealer)
			seed := map[string]string{
				catalogconfig.Source:              config.CatalogSourceStarmap,
				catalogconfig.SourceURL:           "https://catalog.example/api/v1",
				catalogconfig.SourceAPIKey:        "source-credential-value",
				catalogconfig.AcquisitionInterval: "5m",
			}

			preview, err := store.Preview(t.Context(), seed)
			require.NoError(t, err)
			require.Equal(t, configrevision.Head{
				DeploymentID: testDeployment, Namespace: testNamespace, Sequence: 1,
				Authority: configrevision.AuthorityShared, Checksum: config.SharedValuesChecksum(seed),
			}, preview)
			heads, revisions, records := countRows(t, db)
			require.Equal(t, [3]int{0, 0, 0}, [3]int{heads, revisions, records}, "a preview writes nothing")

			// A failed audit write rolls back the head and the revision.
			hide, restore := renameAudit(db, "audit_log", "audit_log_hidden"), renameAudit(db, "audit_log_hidden", "audit_log")
			_, err = db.ExecContext(t.Context(), hide)
			require.NoError(t, err)
			_, err = store.Migrate(t.Context(), configrevision.AuthorityShared, seed, "operator:test", "migrate-shared")
			require.ErrorContains(t, err, "record audit entry")
			var headRows, revisionRows int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM deployment_configuration_head").Scan(&headRows))
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM deployment_configuration_revisions").Scan(&revisionRows))
			require.Equal(t, [2]int{0, 0}, [2]int{headRows, revisionRows})
			_, err = db.ExecContext(t.Context(), restore)
			require.NoError(t, err)
			_, err = store.Head(t.Context())
			require.ErrorIs(t, err, configrevision.ErrNotInitialized)

			written, err := store.Migrate(t.Context(), configrevision.AuthorityShared, seed, "operator:test", "migrate-shared")
			require.NoError(t, err)
			require.Equal(t, int64(1), written.Sequence)
			heads, revisions, records = countRows(t, db)
			require.Equal(t, [3]int{1, 1, 1}, [3]int{heads, revisions, records})
			page, err := trail.List(t.Context(), audit.Query{Action: configrevision.ActionInitialize})
			require.NoError(t, err)
			require.Len(t, page.Records, 1)
			require.Equal(t, written.RevisionID, page.Records[0].Subject)
			require.Equal(t, "operator:test", page.Records[0].Actor)

			var record string
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT record FROM deployment_configuration_revisions").Scan(&record))
			require.NotContains(t, record, "source-credential-value", "the credential is sealed")

			// A repeated operation ID returns the original revision without a new row.
			replayed, err := store.Migrate(t.Context(), configrevision.AuthorityShared, seed, "operator:test", "migrate-shared")
			require.NoError(t, err)
			require.Equal(t, written.Head, replayed.Head)
			_, err = store.Migrate(t.Context(), configrevision.AuthorityShared, map[string]string{catalogconfig.AcquisitionInterval: "9m"}, "operator:test", "migrate-shared")
			require.ErrorIs(t, err, configrevision.ErrOperationConflict)
			heads, revisions, records = countRows(t, db)
			require.Equal(t, [3]int{1, 1, 1}, [3]int{heads, revisions, records})

			// A reload through a new connection reads the revision.
			reloaded, _ := openStore(t, openDatabase(t, selected), sealer)
			current, err := reloaded.Current(t.Context())
			require.NoError(t, err)
			require.Equal(t, seed, current.Values)
			require.Equal(t, written.Head, current.Head)
			unsealed, _ := openStore(t, openDatabase(t, selected), nil)
			head, err := unsealed.Head(t.Context())
			require.NoError(t, err, "the head reads without the master key")
			require.Equal(t, written.Head, head)
			_, err = unsealed.Current(t.Context())
			require.ErrorContains(t, err, "STARPORT_SECURITY_MASTER_KEY")
			var sealed *configrevision.UnsealError
			require.ErrorAs(t, err, &sealed)
			require.Equal(t, catalogconfig.SourceAPIKey, sealed.Setting)
			require.NotErrorIs(t, err, configrevision.ErrCorrupt)

			// Shared to local records the final applied values as a local revision.
			released, err := store.Migrate(t.Context(), configrevision.AuthorityLocal, nil, "operator:test", "migrate-local")
			require.NoError(t, err)
			require.Equal(t, int64(2), released.Sequence)
			require.Equal(t, configrevision.AuthorityLocal, released.Authority)
			require.Equal(t, written.Checksum, released.Checksum)
			_, err = store.Migrate(t.Context(), configrevision.AuthorityLocal, nil, "operator:test", "migrate-local-again")
			require.ErrorContains(t, err, "already released")
			restored, err := store.Migrate(t.Context(), configrevision.AuthorityShared, seed, "operator:test", "migrate-shared-again")
			require.NoError(t, err)
			require.Equal(t, int64(3), restored.Sequence)
			require.Equal(t, configrevision.AuthorityShared, restored.Authority)
			page, err = trail.List(t.Context(), audit.Query{Action: configrevision.ActionCommit})
			require.NoError(t, err)
			require.Len(t, page.Records, 2)
		})
	}
}

func TestCommitRefusesStaleRevisionAndReplaysOperation(t *testing.T) {
	for name, selected := range backendConfigs(t) {
		t.Run(name, func(t *testing.T) {
			db := openDatabase(t, selected)
			store, _ := openStore(t, db, nil)
			_, err := store.Commit(t.Context(), 1, map[string]string{}, "operator:test", "commit-early")
			require.ErrorIs(t, err, configrevision.ErrNotInitialized)
			first, err := store.Initialize(t.Context(), map[string]string{catalogconfig.AcquisitionInterval: "5m"}, "operator:test", "initialize")
			require.NoError(t, err)
			_, err = store.Initialize(t.Context(), map[string]string{}, "operator:test", "initialize-again")
			var initialized *configrevision.InitializedError
			require.ErrorAs(t, err, &initialized)
			require.Equal(t, first.Head, initialized.Head)

			values := map[string]string{catalogconfig.AcquisitionInterval: "7m"}
			second, err := store.Commit(t.Context(), 1, values, "operator:test", "commit-2")
			require.NoError(t, err)
			require.Equal(t, int64(2), second.Sequence)
			replayed, err := store.Commit(t.Context(), 1, values, "operator:test", "commit-2")
			require.NoError(t, err)
			require.Equal(t, second.Head, replayed.Head)
			_, err = store.Commit(t.Context(), 1, map[string]string{catalogconfig.AcquisitionInterval: "8m"}, "operator:test", "commit-2")
			require.ErrorIs(t, err, configrevision.ErrOperationConflict)

			_, err = store.Commit(t.Context(), 1, values, "operator:test", "commit-stale")
			var stale *configrevision.StaleRevisionError
			require.ErrorAs(t, err, &stale)
			require.Equal(t, configrevision.StaleRevisionError{Expected: 1, Current: 2}, *stale)

			// Rollback is a new revision with the previous values.
			rollback, err := store.Commit(t.Context(), 2, map[string]string{catalogconfig.AcquisitionInterval: "5m"}, "operator:test", "commit-rollback")
			require.NoError(t, err)
			require.Equal(t, int64(3), rollback.Sequence)
			require.Equal(t, first.Checksum, rollback.Checksum)
			heads, revisions, records := countRows(t, db)
			require.Equal(t, [3]int{1, 3, 3}, [3]int{heads, revisions, records})

			_, err = store.Commit(t.Context(), 3, map[string]string{"STARPORT_STORAGE_MODE": "valkey"}, "operator:test", "commit-bootstrap")
			var refused *config.SharedSettingError
			require.ErrorAs(t, err, &refused)
			require.Equal(t, "STARPORT_STORAGE_MODE", refused.Name)

			trail, err := audit.Open(db, 0)
			require.NoError(t, err)
			// The head of the deployment names another namespace.
			other, err := configrevision.New(db, trail, nil, testDeployment, "namespace-b")
			require.NoError(t, err)
			_, err = other.Head(t.Context())
			var authority *config.AuthorityMismatchError
			require.ErrorAs(t, err, &authority)
			require.Equal(t, config.AuthorityMismatchError{Setting: config.NamespaceSetting, Configured: "namespace-b", Stored: testNamespace, DeploymentID: testDeployment}, *authority)
			require.ErrorContains(t, err, testNamespace)
			require.ErrorContains(t, err, testDeployment)
			_, err = other.Initialize(t.Context(), map[string]string{}, "operator:test", "initialize-namespace-b")
			require.ErrorAs(t, err, &authority)

			// Another deployment has no head. The error names the stored deployment.
			absent, err := configrevision.New(db, trail, nil, "deployment-b", "namespace-b")
			require.NoError(t, err)
			_, err = absent.Head(t.Context())
			require.ErrorIs(t, err, configrevision.ErrNotInitialized)
			var notInitialized *configrevision.NotInitializedError
			require.ErrorAs(t, err, &notInitialized)
			require.Equal(t, configrevision.NotInitializedError{DeploymentID: "deployment-b", Stored: []string{testDeployment}, Others: 1}, *notInitialized)
			require.ErrorContains(t, err, `"`+testDeployment+`"`)
			require.ErrorContains(t, err, "starport config init --shared")

			// A namespace belongs to one deployment.
			taken, err := configrevision.New(db, trail, nil, "deployment-b", testNamespace)
			require.NoError(t, err)
			_, err = taken.Initialize(t.Context(), map[string]string{}, "operator:test", "initialize-taken")
			require.Error(t, err)
			heads, revisions, records = countRows(t, db)
			require.Equal(t, [3]int{1, 3, 3}, [3]int{heads, revisions, records})
		})
	}
}

func TestCommitAbortsWithoutAuditRecord(t *testing.T) {
	for name, selected := range backendConfigs(t) {
		t.Run(name, func(t *testing.T) {
			db := openDatabase(t, selected)
			sealer, err := credentials.NewEncryptionService([]byte(strings.Repeat("k", 32)))
			require.NoError(t, err)
			store, trail := openStore(t, db, sealer)
			first, err := store.Initialize(t.Context(), map[string]string{catalogconfig.AcquisitionInterval: "5m"}, "operator:test", "initialize")
			require.NoError(t, err)
			require.NoError(t, store.CheckSchema(t.Context()))

			// The audit table is gone, so the transaction cannot record the commit.
			_, err = db.ExecContext(t.Context(), renameAudit(db, "audit_log", "audit_log_hidden"))
			require.NoError(t, err)
			values := map[string]string{catalogconfig.AcquisitionInterval: "7m"}
			_, err = store.Commit(t.Context(), first.Sequence, values, "operator:test", "commit-without-audit")
			require.ErrorContains(t, err, "record audit entry")
			head, err := store.Head(t.Context())
			require.NoError(t, err)
			require.Equal(t, first.Head, head, "the head did not move")
			var revisions int
			require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM deployment_configuration_revisions").Scan(&revisions))
			require.Equal(t, 1, revisions, "the revision row rolled back")
			_, found, err := store.OperationRevision(t.Context(), "commit-without-audit")
			require.NoError(t, err)
			require.False(t, found, "the operation ID committed nothing")

			// With the audit table back, the same operation commits once with its record.
			_, err = db.ExecContext(t.Context(), renameAudit(db, "audit_log_hidden", "audit_log"))
			require.NoError(t, err)
			committed, err := store.Commit(t.Context(), first.Sequence, values, "operator:test", "commit-without-audit")
			require.NoError(t, err)
			require.Equal(t, first.Sequence+1, committed.Sequence)
			stored, found, err := store.OperationRevision(t.Context(), "commit-without-audit")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, values, stored.Values)
			page, err := trail.List(t.Context(), audit.Query{Action: configrevision.ActionCommit})
			require.NoError(t, err)
			require.Len(t, page.Records, 1)
			require.Equal(t, committed.RevisionID, page.Records[0].Subject)
			heads, revisions, records := countRows(t, db)
			require.Equal(t, [3]int{1, 2, 2}, [3]int{heads, revisions, records})
		})
	}
}

func TestSealedCredentialFailsClosed(t *testing.T) {
	for name, selected := range backendConfigs(t) {
		t.Run(name, func(t *testing.T) {
			db := openDatabase(t, selected)
			sealer, err := credentials.NewEncryptionService([]byte(strings.Repeat("a", 32)))
			require.NoError(t, err)
			store, _ := openStore(t, db, sealer)
			const secret = "source-token-value"
			values := map[string]string{
				catalogconfig.Source:              config.CatalogSourceGitHub,
				catalogconfig.SourceToken:         secret,
				catalogconfig.AcquisitionInterval: "5m",
			}
			// Without a master key, a seed with a credential refuses before any write.
			unkeyed, _ := openStore(t, db, nil)
			_, err = unkeyed.Initialize(t.Context(), values, "operator:test", "initialize-unkeyed")
			require.ErrorContains(t, err, "cannot store "+catalogconfig.SourceToken+" without STARPORT_SECURITY_MASTER_KEY")
			require.NotContains(t, err.Error(), secret)
			heads, revisions, records := countRows(t, db)
			require.Equal(t, [3]int{0, 0, 0}, [3]int{heads, revisions, records})

			written, err := store.Initialize(t.Context(), values, "operator:test", "initialize-sealed")
			require.NoError(t, err)

			// Without a master key, a revision with a credential refuses.
			const key = "source-api-key-value"
			_, err = unkeyed.Commit(t.Context(), written.Sequence, map[string]string{
				catalogconfig.Source:       config.CatalogSourceStarmap,
				catalogconfig.SourceURL:    "https://catalog.example/api/v1",
				catalogconfig.SourceAPIKey: key,
			}, "operator:test", "commit-unkeyed")
			require.ErrorContains(t, err, "cannot store "+catalogconfig.SourceAPIKey+" without STARPORT_SECURITY_MASTER_KEY")
			require.NotContains(t, err.Error(), key)
			heads, revisions, records = countRows(t, db)
			require.Equal(t, [3]int{1, 1, 1}, [3]int{heads, revisions, records})

			// Another master key reads the head but cannot open the credential.
			other, err := credentials.NewEncryptionService([]byte(strings.Repeat("b", 32)))
			require.NoError(t, err)
			rotated, _ := openStore(t, db, other)
			head, err := rotated.Head(t.Context())
			require.NoError(t, err)
			require.Equal(t, written.Head, head)
			current, err := rotated.Current(t.Context())
			var sealed *configrevision.UnsealError
			require.ErrorAs(t, err, &sealed)
			require.Equal(t, catalogconfig.SourceToken, sealed.Setting)
			require.NotErrorIs(t, err, configrevision.ErrCorrupt, "the checksum matched")
			require.NotContains(t, err.Error(), secret)
			require.Empty(t, current.Values, "a refused read returns no credential")

			// A checksum that differs from the values is corrupt, not sealed.
			_, err = db.ExecContext(t.Context(), db.Bind("UPDATE deployment_configuration_revisions SET checksum = ? WHERE revision_id = ?"), strings.Repeat("0", 64), written.RevisionID)
			require.NoError(t, err)
			_, err = store.Current(t.Context())
			require.ErrorIs(t, err, configrevision.ErrCorrupt)
			require.False(t, errors.As(err, &sealed))
			require.NotContains(t, err.Error(), secret)
		})
	}
}

func renameAudit(db *sqlstore.DB, from, to string) string {
	if db.Dialect() == sqlstore.TypeMySQL {
		return "RENAME TABLE " + from + " TO " + to
	}
	return "ALTER TABLE " + from + " RENAME TO " + to
}
