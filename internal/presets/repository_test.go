package presets

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPresetRepositoryContract(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		ctx := context.Background()
		repository, err := Open(store)
		require.NoError(t, err)
		suffix := uuid.NewString()
		preset := Preset{
			ID:        "preset-id-" + suffix,
			Name:      "preset-" + suffix,
			Config:    map[string]any{"model": "openai/gpt-4o"},
			Version:   1,
			CreatedAt: time.Unix(100, 0).UTC(),
			UpdatedAt: time.Unix(100, 0).UTC(),
		}

		created, err := repository.Create(ctx, preset)
		require.NoError(t, err)
		require.EqualValues(t, 1, created.Revision)
		stored, err := repository.Get(ctx, preset.Name)
		require.NoError(t, err)
		require.Equal(t, created, stored)

		keys, err := store.ScanWithPrefix(ctx, StoragePrefix, 0)
		require.NoError(t, err)
		require.Equal(t, []string{storageKey(preset.Name)}, keys)
		data, err := store.Get(ctx, keys[0])
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(data, &schema))
		require.EqualValues(t, StorageSchemaVersion, schema["schema_version"])

		_, err = repository.Create(ctx, preset)
		require.ErrorIs(t, err, ErrConflict)
		listed, err := repository.List(ctx, 10)
		require.NoError(t, err)
		require.Len(t, listed, 1)

		preset.Version = 2
		preset.Config["temperature"] = 0.5
		updated, err := repository.Update(ctx, preset, created.Revision)
		require.NoError(t, err)
		require.EqualValues(t, 2, updated.Revision)
		_, err = repository.Update(ctx, preset, created.Revision)
		require.ErrorIs(t, err, ErrConflict)

		require.ErrorIs(t, repository.Delete(ctx, preset.Name, created.Revision), ErrConflict)
		require.NoError(t, repository.Delete(ctx, preset.Name, updated.Revision))
		_, err = repository.Get(ctx, preset.Name)
		require.ErrorIs(t, err, ErrNotFound)

		corruptName := "corrupt-" + suffix
		require.NoError(t, store.Set(ctx, storageKey(corruptName), []byte(`{"schema_version":2}`)))
		_, err = repository.Get(ctx, corruptName)
		require.True(t, errors.Is(err, ErrCorruptRecord))
	})
}
