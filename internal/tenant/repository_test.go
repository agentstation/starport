package tenant

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestTenantRepositoryContract(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)

		created := Tenant{
			ID:                 "acme",
			Name:               "Acme",
			CredentialStrategy: StrategyBYOKOnly,
			Limits: &limits.Limits{
				Spend: &limits.Budget{Limit: 1000, Interval: limits.IntervalMonth},
			},
			Active:    true,
			CreatedAt: time.Unix(100, 0).UTC(),
			UpdatedAt: time.Unix(100, 0).UTC(),
		}

		record, err := repository.Create(ctx, created)
		require.NoError(t, err)
		require.EqualValues(t, 1, record.Revision)

		stored, err := repository.GetByID(ctx, created.ID)
		require.NoError(t, err)
		require.Equal(t, record, stored)
		require.Equal(t, StrategyBYOKOnly, stored.Tenant.CredentialStrategy)
		require.EqualValues(t, 1000, stored.Tenant.Limits.Spend.Limit)

		// The durable value carries its schema version, so a later reader can
		// tell a v1 record from a future one without guessing.
		data, err := store.Get(ctx, tenantStorageKey(created.ID))
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(data, &schema))
		require.EqualValues(t, StorageSchemaVersion, schema["schema_version"])

		_, err = repository.Create(ctx, created)
		require.ErrorIs(t, err, ErrConflict)

		// Limits are cloned in and out, so a caller cannot reach into the
		// stored record through the pointer it handed over.
		created.Limits.Spend.Limit = 5
		reread, err := repository.GetByID(ctx, created.ID)
		require.NoError(t, err)
		require.EqualValues(t, 1000, reread.Tenant.Limits.Spend.Limit)

		renamed := reread.Tenant
		renamed.Name = "Acme Corp"
		renamed.CreatedAt = time.Unix(999, 0).UTC()
		updated, err := repository.Update(ctx, renamed, reread.Revision)
		require.NoError(t, err)
		require.EqualValues(t, 2, updated.Revision)
		require.Equal(t, "Acme Corp", updated.Tenant.Name)
		// Creation time belongs to the record, not to the caller's payload.
		require.Equal(t, reread.Tenant.CreatedAt, updated.Tenant.CreatedAt)

		_, err = repository.Update(ctx, renamed, reread.Revision)
		require.ErrorIs(t, err, ErrConflict)

		require.ErrorIs(t, repository.Delete(ctx, created.ID, reread.Revision), ErrConflict)
		require.NoError(t, repository.Delete(ctx, created.ID, updated.Revision))
		_, err = repository.GetByID(ctx, created.ID)
		require.ErrorIs(t, err, ErrNotFound)
	})
}

func TestEnsureDefaultIsIdempotent(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)

		first, err := repository.EnsureDefault(ctx)
		require.NoError(t, err)
		require.Equal(t, DefaultID, first.Tenant.ID)
		require.True(t, first.Tenant.IsDefault())
		require.True(t, first.Tenant.Active)
		require.Equal(t, StrategyOperatorFirst, first.Tenant.CredentialStrategy)

		// Every boot calls this. A second call must observe the first tenant
		// rather than create a second one or bump its revision.
		second, err := repository.EnsureDefault(ctx)
		require.NoError(t, err)
		require.Equal(t, first, second)

		listed, err := repository.List(ctx, 10, 0)
		require.NoError(t, err)
		require.Len(t, listed, 1)
	})
}

func TestEnsureDefaultUnderConcurrentBoot(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)

		// Two processes can boot against one store. The loser of the create
		// race must read the winner instead of failing startup.
		const bootCount = 8
		results := make([]Record, bootCount)
		errs := make([]error, bootCount)
		var start sync.WaitGroup
		var done sync.WaitGroup
		start.Add(1)
		for index := range bootCount {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				results[index], errs[index] = repository.EnsureDefault(ctx)
			}()
		}
		start.Done()
		done.Wait()

		for index := range bootCount {
			require.NoErrorf(t, errs[index], "boot %d", index)
			require.Equal(t, DefaultID, results[index].Tenant.ID)
			require.EqualValues(t, 1, results[index].Revision)
		}

		listed, err := repository.List(ctx, 10, 0)
		require.NoError(t, err)
		require.Len(t, listed, 1)
	})
}

func TestDefaultTenantCannotBeDeleted(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)

		record, err := repository.EnsureDefault(ctx)
		require.NoError(t, err)

		// A gateway API key with no explicit tenant resolves here. Removing it
		// would strand those keys, so the repository refuses even a correct
		// revision.
		require.ErrorIs(t, repository.Delete(ctx, DefaultID, record.Revision), ErrDefaultImmutable)
		_, err = repository.GetByID(ctx, DefaultID)
		require.NoError(t, err)
	})
}

func TestRepositoryRejectsInvalidTenants(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)

		valid := Tenant{ID: "acme", Name: "Acme", Active: true}

		// A tenant ID reaches a credential storage key and a log field, so a
		// separator or a wildcard must never enter one.
		for _, id := range []string{"", "acme corp", "acme/eu", "*", "tenant:acme"} {
			invalid := valid
			invalid.ID = id
			_, err := repository.Create(ctx, invalid)
			require.Errorf(t, err, "tenant ID %q must be rejected", id)
		}

		unnamed := valid
		unnamed.Name = ""
		_, err = repository.Create(ctx, unnamed)
		require.ErrorIs(t, err, ErrInvalidName)

		unknownStrategy := valid
		unknownStrategy.CredentialStrategy = CredentialStrategy("gateway_only")
		_, err = repository.Create(ctx, unknownStrategy)
		require.ErrorIs(t, err, ErrInvalidCredentialStrategy)

		badLimits := valid
		badLimits.Limits = &limits.Limits{Spend: &limits.Budget{Limit: 1, Interval: "hour"}}
		_, err = repository.Create(ctx, badLimits)
		require.ErrorIs(t, err, limits.ErrInvalidBudgetInterval)

		listed, err := repository.List(ctx, 10, 0)
		require.NoError(t, err)
		require.Empty(t, listed)
	})
}

func TestCredentialStrategyPolicy(t *testing.T) {
	// An unset strategy reads as the default rather than as a denial, so a
	// record written before the field existed still reaches an operator
	// credential.
	require.Equal(t, StrategyOperatorFirst, Tenant{}.EffectiveCredentialStrategy())
	require.Equal(
		t,
		StrategyBYOKFirst,
		Tenant{CredentialStrategy: StrategyBYOKFirst}.EffectiveCredentialStrategy(),
	)

	require.True(t, StrategyOperatorFirst.AllowsOperatorCredentials())
	require.True(t, StrategyBYOKFirst.AllowsOperatorCredentials())
	// This is the value an operator sets to deny an account the deployment's
	// own provider credentials.
	require.False(t, StrategyBYOKOnly.AllowsOperatorCredentials())
}
