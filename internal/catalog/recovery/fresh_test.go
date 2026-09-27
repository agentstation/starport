package recovery

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func freshTestStores(t *testing.T) (*Witness, storage.KVStore, storage.FreshDatabase) {
	t.Helper()
	sqlURL, kvURL := os.Getenv("TEST_POSTGRES_URL"), os.Getenv("TEST_VALKEY_URL")
	if sqlURL == "" || kvURL == "" {
		t.Skip("UNVERIFIED: real PostgreSQL and Valkey are required")
	}
	cfg := isolatedWitnessPostgres(t, sqlstore.Config{Type: sqlstore.TypePostgres, Postgres: sqlstore.PostgresConfig{URL: sqlURL}})
	db, err := sqlstore.Open(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Migrate(t.Context()))
	w, err := New(db)
	require.NoError(t, err)
	kv, err := storage.OpenValkey(storage.ValkeyConfig{DeploymentID: "contract-tests", URL: kvURL, DB: 14})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, kv.Close()) })
	keys, err := kv.ScanWithPrefix(t.Context(), "", 100)
	require.NoError(t, err)
	require.Empty(t, keys, "fresh initialization fixture needs exclusive empty Valkey database 14")
	t.Cleanup(func() {
		require.NoError(t, kv.BatchDelete(context.Background(), []string{"catalog:bootstrap:v1", "existing"}))
	})
	return w, kv, kv.(storage.FreshDatabase)
}

func TestFreshFleetInitialization(t *testing.T) {
	w, kv, backend := freshTestStores(t)
	request := FreshRequest{OperationID: "fresh", Evidence: "test/procedure"}
	record, err := w.InitializeFresh(t.Context(), backend, "test-deployment", request)
	require.NoError(t, err)
	actual, err := w.Approved(t.Context(), record.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, record, actual)
	_, err = backend.BindIncarnation(t.Context(), record.BackendID)
	require.NoError(t, err)
	claim, err := kv.Get(t.Context(), "catalog:bootstrap:v1")
	require.NoError(t, err)
	require.NotEmpty(t, claim)
	_, err = w.InitializeFresh(t.Context(), backend, "test-deployment", request)
	require.ErrorIs(t, err, sqlstore.ErrNotFresh)
	actual, err = w.Approved(t.Context(), record.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, record, actual)
}

func TestFreshFleetRefusesExistingState(t *testing.T) {
	for _, state := range []string{"kv", "sql", "approval", "closed-approval", "row-security", "unknown-table"} {
		t.Run(state, func(t *testing.T) {
			w, kv, backend := freshTestStores(t)
			switch state {
			case "kv":
				require.NoError(t, kv.Set(t.Context(), "existing", []byte("preserved")))
			case "sql":
				_, err := w.db.ExecContext(t.Context(), "INSERT INTO users (id,subject,revision,record) VALUES ('existing','existing',1,'{}')")
				require.NoError(t, err)
			case "closed-approval", "approval":
				record, err := w.Initialize(t.Context(), "test-deployment")
				require.NoError(t, err)
				if state == "approval" {
					_, err = w.Approve(t.Context(), record, "prior-backend", "prior-proof")
					require.NoError(t, err)
				}
			case "row-security":
				_, err := w.db.ExecContext(t.Context(), "ALTER TABLE users ENABLE ROW LEVEL SECURITY")
				require.NoError(t, err)
			case "unknown-table":
				_, err := w.db.ExecContext(t.Context(), "CREATE TABLE unexpected (value TEXT); INSERT INTO unexpected VALUES ('preserved')")
				require.NoError(t, err)
			}
			_, err := w.InitializeFresh(t.Context(), backend, "test-deployment", FreshRequest{OperationID: "fresh", Evidence: "test/procedure"})
			require.Error(t, err)
			_, err = kv.Get(t.Context(), "catalog:bootstrap:v1")
			require.ErrorIs(t, err, storage.ErrNotFound)
			if state == "kv" {
				value, err := kv.Get(t.Context(), "existing")
				require.NoError(t, err)
				require.Equal(t, []byte("preserved"), value)
			}
			if state != "approval" {
				_, err = w.Approved(t.Context(), "test-deployment")
				require.ErrorIs(t, err, ErrClosed)
			}
		})
	}
}

type interruptedFreshBackend struct {
	storage.FreshDatabase
	failure error
}

func (b interruptedFreshBackend) ClaimEmptyDatabase(ctx context.Context, id, key string, claim []byte) error {
	if err := b.FreshDatabase.ClaimEmptyDatabase(ctx, id, key, claim); err != nil {
		return err
	}
	return b.failure
}

func TestFreshFleetInterruptedClaim(t *testing.T) {
	w, _, backend := freshTestStores(t)
	request := FreshRequest{OperationID: "interrupted", Evidence: "test/procedure"}
	failure := errors.New("reply lost after native claim")
	_, err := w.InitializeFresh(t.Context(), interruptedFreshBackend{backend, failure}, "test-deployment", request)
	require.ErrorIs(t, err, failure)
	_, err = w.Approved(t.Context(), "test-deployment")
	require.ErrorIs(t, err, ErrClosed)
	other := request
	other.OperationID = "different"
	_, err = w.InitializeFresh(t.Context(), backend, "test-deployment", other)
	require.ErrorIs(t, err, storage.ErrDatabaseNotEmpty)
	_, err = w.InitializeFresh(t.Context(), backend, "test-deployment", request)
	require.NoError(t, err)
}

func TestFreshFleetConcurrentInitialization(t *testing.T) {
	w, _, backend := freshTestStores(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, op := range []string{"first", "second"} {
		wg.Go(func() {
			_, err := w.InitializeFresh(t.Context(), backend, "test-deployment", FreshRequest{OperationID: op, Evidence: "test/procedure"})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, sqlstore.ErrNotFresh)
		}
	}
	require.Equal(t, 1, success)
	record, err := w.Approved(t.Context(), "test-deployment")
	require.NoError(t, err)
	require.Equal(t, int64(1), record.Epoch)
}

func TestFreshFleetRefusesChangedIncarnation(t *testing.T) {
	_, kv, backend := freshTestStores(t)
	identity, err := backend.ObserveIncarnation(t.Context())
	require.NoError(t, err)
	replacement := []byte(identity)
	if replacement[0] == 'a' {
		replacement[0] = 'b'
	} else {
		replacement[0] = 'a'
	}
	err = backend.ClaimEmptyDatabase(t.Context(), string(replacement), "catalog:bootstrap:v1", []byte("claim"))
	require.ErrorIs(t, err, storage.ErrIncarnationChanged)
	_, err = kv.Get(t.Context(), "catalog:bootstrap:v1")
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestFreshFleetSQLFailureRetainsOnlyRetryClaim(t *testing.T) {
	w, kv, backend := freshTestStores(t)
	_, err := w.db.ExecContext(t.Context(), `CREATE FUNCTION refuse_approval() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected SQL refusal'; END $$;
CREATE TRIGGER refuse_approval BEFORE INSERT ON catalog_recovery FOR EACH ROW EXECUTE FUNCTION refuse_approval()`)
	require.NoError(t, err)
	request := FreshRequest{OperationID: "sql-interrupted", Evidence: "test/procedure"}
	_, err = w.InitializeFresh(t.Context(), backend, "test-deployment", request)
	require.ErrorContains(t, err, "injected SQL refusal")
	_, err = w.Approved(t.Context(), "test-deployment")
	require.ErrorIs(t, err, ErrClosed)
	_, err = kv.Get(t.Context(), "catalog:bootstrap:v1")
	require.NoError(t, err)
	_, err = w.db.ExecContext(t.Context(), "DROP TRIGGER refuse_approval ON catalog_recovery; DROP FUNCTION refuse_approval()")
	require.NoError(t, err)
	require.NoError(t, kv.Set(t.Context(), "existing", []byte("new-state")))
	_, err = w.InitializeFresh(t.Context(), backend, "test-deployment", request)
	require.ErrorIs(t, err, storage.ErrDatabaseNotEmpty)
	require.NoError(t, kv.Delete(t.Context(), "existing"))
	_, err = w.InitializeFresh(t.Context(), backend, "test-deployment", request)
	require.NoError(t, err)
}
