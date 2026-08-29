package account

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

func TestAccountRepositoryContract(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)

		created := Account{
			ID:                 "acme",
			Name:               "Acme",
			CredentialStrategy: StrategyBYOKOnly,
			Limits: &limits.Limits{
				Spend: &limits.Budget{Limit: 1000, Interval: limits.IntervalMonth},
			},
			Active: true,
		}

		record, err := repository.Create(ctx, created)
		require.NoError(t, err)
		require.EqualValues(t, 1, record.Revision)
		// The repository stamps creation time; a caller that omits it must
		// never persist the zero time the console would render as a date.
		require.False(t, record.Account.CreatedAt.IsZero())
		require.False(t, record.Account.UpdatedAt.IsZero())

		stored, err := repository.GetByID(ctx, created.ID)
		require.NoError(t, err)
		require.Equal(t, record, stored)
		require.Equal(t, StrategyBYOKOnly, stored.Account.CredentialStrategy)
		require.EqualValues(t, 1000, stored.Account.Limits.Spend.Limit)

		// The durable value carries its schema version, so a later reader can
		// tell a v1 record from a future one without guessing.
		data, err := store.Get(ctx, accountStorageKey(created.ID))
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
		require.EqualValues(t, 1000, reread.Account.Limits.Spend.Limit)

		renamed := reread.Account
		renamed.Name = "Acme Corp"
		renamed.CreatedAt = time.Unix(999, 0).UTC()
		updated, err := repository.Update(ctx, renamed, reread.Revision)
		require.NoError(t, err)
		require.EqualValues(t, 2, updated.Revision)
		require.Equal(t, "Acme Corp", updated.Account.Name)
		// Creation time belongs to the record, not to the caller's payload.
		require.Equal(t, reread.Account.CreatedAt, updated.Account.CreatedAt)

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
		require.Equal(t, DefaultID, first.Account.ID)
		require.True(t, first.Account.IsDefault())
		require.True(t, first.Account.Active)
		require.Equal(t, StrategyOperatorFirst, first.Account.CredentialStrategy)

		// Every boot calls this. A second call must observe the first account
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
			require.Equal(t, DefaultID, results[index].Account.ID)
			require.EqualValues(t, 1, results[index].Revision)
		}

		listed, err := repository.List(ctx, 10, 0)
		require.NoError(t, err)
		require.Len(t, listed, 1)
	})
}

func TestDefaultAccountCannotBeDeleted(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)

		record, err := repository.EnsureDefault(ctx)
		require.NoError(t, err)

		// A gateway API key with no explicit account resolves here. Removing it
		// would strand those keys, so the repository refuses even a correct
		// revision.
		require.ErrorIs(t, repository.Delete(ctx, DefaultID, record.Revision), ErrDefaultImmutable)
		_, err = repository.GetByID(ctx, DefaultID)
		require.NoError(t, err)
	})
}

func TestRepositoryRejectsInvalidAccounts(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)

		valid := Account{ID: "acme", Name: "Acme", Active: true}

		// An account ID reaches a credential storage key and a log field, so a
		// separator or a wildcard must never enter one.
		for _, id := range []string{"", "acme corp", "acme/eu", "*", "account:acme"} {
			invalid := valid
			invalid.ID = id
			_, err := repository.Create(ctx, invalid)
			require.Errorf(t, err, "account ID %q must be rejected", id)
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
	require.Equal(t, StrategyOperatorFirst, Account{}.EffectiveCredentialStrategy())
	require.Equal(
		t,
		StrategyBYOKFirst,
		Account{CredentialStrategy: StrategyBYOKFirst}.EffectiveCredentialStrategy(),
	)

	require.True(t, StrategyOperatorFirst.AllowsOperatorCredentials())
	require.True(t, StrategyBYOKFirst.AllowsOperatorCredentials())
	// This is the value an operator sets to deny an account the deployment's
	// own provider credentials.
	require.False(t, StrategyBYOKOnly.AllowsOperatorCredentials())
}
