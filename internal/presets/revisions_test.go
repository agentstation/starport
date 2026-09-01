package presets

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPresetRevisionHistoryAndRollback(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)
		name := "preset-" + uuid.NewString()
		preset := Preset{
			Name:        name,
			Description: "first",
			Config:      Config{Model: "openai/gpt-4o"},
			CreatedAt:   time.Unix(100, 0).UTC(),
			UpdatedAt:   time.Unix(100, 0).UTC(),
		}

		// Three saves yield revisions 1 through 3.
		created, err := repository.Create(ctx, preset)
		require.NoError(t, err)
		require.EqualValues(t, 1, created.Revision)

		preset.Description = "second"
		preset.Config.Model = "openai/gpt-4o-mini"
		second, err := repository.Update(ctx, preset, 1)
		require.NoError(t, err)
		require.EqualValues(t, 2, second.Revision)

		preset.Description = "third"
		preset.Config.Model = "anthropic/claude-sonnet-4"
		third, err := repository.Update(ctx, preset, 2)
		require.NoError(t, err)
		require.EqualValues(t, 3, third.Revision)

		history, err := repository.History(ctx, name, 0)
		require.NoError(t, err)
		require.Len(t, history, 3)
		require.EqualValues(t, 3, history[0].Revision)
		require.EqualValues(t, 2, history[1].Revision)
		require.EqualValues(t, 1, history[2].Revision)
		require.Equal(t, "third", history[0].Preset.Description)
		require.Equal(t, "first", history[2].Preset.Description)

		limited, err := repository.History(ctx, name, 2)
		require.NoError(t, err)
		require.Len(t, limited, 2)
		require.EqualValues(t, 3, limited[0].Revision)

		// A pinned read answers the stored revision verbatim.
		pinned, err := repository.GetRevision(ctx, name, 2)
		require.NoError(t, err)
		require.EqualValues(t, 2, pinned.Revision)
		require.Equal(t, "second", pinned.Preset.Description)
		require.Equal(t, "openai/gpt-4o-mini", pinned.Preset.Config.Model)

		_, err = repository.GetRevision(ctx, name, 9)
		require.ErrorIs(t, err, ErrNotFound)

		// Rollback to revision 1 creates revision 4 that copies it.
		_, err = repository.Rollback(ctx, name, 1, 1)
		require.ErrorIs(t, err, ErrConflict)

		rolled, err := repository.Rollback(ctx, name, 1, 3)
		require.NoError(t, err)
		require.EqualValues(t, 4, rolled.Revision)
		require.Equal(t, "first", rolled.Preset.Description)
		require.Equal(t, "openai/gpt-4o", rolled.Preset.Config.Model)
		require.Equal(t, created.Preset.Config, rolled.Preset.Config)
		require.Equal(t, created.Preset.CreatedAt, rolled.Preset.CreatedAt)

		head, err := repository.Get(ctx, name)
		require.NoError(t, err)
		require.Equal(t, rolled, head)

		history, err = repository.History(ctx, name, 0)
		require.NoError(t, err)
		require.Len(t, history, 4)
		require.EqualValues(t, 4, history[0].Revision)

		// Delete drops the history with the head.
		require.NoError(t, repository.Delete(ctx, name, 4))
		keys, err := store.ScanWithPrefix(ctx, revisionScope(name), 0)
		require.NoError(t, err)
		require.Empty(t, keys)
		history, err = repository.History(ctx, name, 0)
		require.NoError(t, err)
		require.Empty(t, history)
	})
}

func TestPresetRollbackNeedsATarget(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)
		name := "preset-" + uuid.NewString()
		created, err := repository.Create(ctx, Preset{
			Name:      name,
			Config:    Config{Model: "openai/gpt-4o"},
			CreatedAt: time.Unix(100, 0).UTC(),
			UpdatedAt: time.Unix(100, 0).UTC(),
		})
		require.NoError(t, err)

		_, err = repository.Rollback(ctx, name, 5, created.Revision)
		require.ErrorIs(t, err, ErrNotFound)
		_, err = repository.Rollback(ctx, "absent-"+uuid.NewString(), 1, 1)
		require.ErrorIs(t, err, ErrNotFound)
		_, err = repository.Rollback(ctx, name, 0, created.Revision)
		require.ErrorIs(t, err, ErrInvalidPreset)
	})
}
