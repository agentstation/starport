package files

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type fileReplaySource map[string]storage.TransferRecord

func (s fileReplaySource) ReadCaptured(ctx context.Context, key string, maximum int) (storage.TransferRecord, error) {
	if err := ctx.Err(); err != nil {
		return storage.TransferRecord{}, err
	}
	raw, ok := s[key]
	if !ok {
		return raw, storage.ErrNotFound
	}
	if len(raw.Value) > maximum {
		return raw, storage.ErrValueTooLarge
	}
	return raw, nil
}
func recoveryFileEvidence(t *testing.T, file File) RecoveryFile {
	t.Helper()
	data, err := encodeFile(file)
	require.NoError(t, err)
	var r RecoveryFile
	require.NoError(t, r.UnmarshalJSON(data))
	return r
}

func TestFileReplayPreservesPrivateIdentityExpiryAndMetering(t *testing.T) {
	at := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	before := sampleFile("owner", "file")
	before.State = FileStatePending
	before.CreatedAt = at.Add(-time.Hour)
	before.ExpiresAt = at.Add(time.Hour)
	before.Bytes = 0
	before.metered = true
	prior := recoveryFileEvidence(t, before)
	key := storageKey(before.Account, before.ID)
	source := fileReplaySource{key: {Key: key, Value: prior.state.raw}}
	assets, err := blob.NewFilesystem(filepath.Join(t.TempDir(), "assets"))
	require.NoError(t, err)
	_, err = assets.Publish(t.Context(), before.blobKey, strings.NewReader("bytes"))
	require.NoError(t, err)
	later := before
	later.Bytes = 5
	later.State = FileStateReady
	next := recoveryFileEvidence(t, later)
	change := RecoveryChange{Account: before.Account, ID: before.ID, ExpectedSHA256: prior.SHA256(), After: &next}
	mutations, err := PrepareRecoveryReplay(t.Context(), source, assets, at, []RecoveryChange{change})
	require.NoError(t, err)
	require.Len(t, mutations, 1)
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		require.NoError(t, store.Set(t.Context(), key, prior.state.raw))
		require.NoError(t, store.CompareAndSwapBatch(t.Context(), mutations))
		raw, err := store.Get(t.Context(), key)
		require.NoError(t, err)
		var retained RecoveryFile
		require.NoError(t, retained.UnmarshalJSON(raw))
		require.Equal(t, later, retained.state.file)
	})
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d", "%p"} {
		require.NotContains(t, fmt.Sprintf(verb, next), before.blobKey)
		require.NotContains(t, fmt.Sprintf(verb, &next), before.blobKey)
	}
	data, err := json.Marshal(next)
	require.NoError(t, err)
	require.Contains(t, string(data), before.blobKey)
	for name, edit := range map[string]func(*File){"expiry": func(f *File) { f.ExpiresAt = f.ExpiresAt.Add(time.Hour) }, "metering": func(f *File) { f.metered = false }, "blob": func(f *File) { f.blobKey = "other" }, "creation": func(f *File) { f.CreatedAt = f.CreatedAt.Add(time.Second) }} {
		t.Run(name, func(t *testing.T) {
			candidate := later
			edit(&candidate)
			r := recoveryFileEvidence(t, candidate)
			c := change
			c.After = &r
			_, err := PrepareRecoveryReplay(t.Context(), source, assets, at, []RecoveryChange{c})
			require.Error(t, err)
		})
	}
	changed := change
	changed.ExpectedSHA256 = ""
	_, err = PrepareRecoveryReplay(t.Context(), source, assets, at, []RecoveryChange{changed})
	require.Error(t, err)
}

func TestFileReplayRequiresRetirementForDeletionAndRealBytesForReady(t *testing.T) {
	at := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	file := sampleFile("owner", "file")
	file.CreatedAt = at.Add(-time.Hour)
	file.ExpiresAt = at.Add(time.Hour)
	file.Bytes = 5
	current := recoveryFileEvidence(t, file)
	key := storageKey(file.Account, file.ID)
	source := fileReplaySource{key: {Key: key, Value: current.state.raw}}
	assets, err := blob.NewFilesystem(filepath.Join(t.TempDir(), "assets"))
	require.NoError(t, err)
	_, err = PrepareRecoveryReplay(t.Context(), fileReplaySource{}, assets, at, []RecoveryChange{{Account: file.Account, ID: file.ID, After: &current}})
	require.Error(t, err)
	deletion := RecoveryChange{Account: file.Account, ID: file.ID, ExpectedSHA256: current.SHA256()}
	_, err = PrepareRecoveryReplay(t.Context(), source, assets, at, []RecoveryChange{deletion})
	require.Error(t, err, "absence cannot prove retirement")
	_, err = assets.Publish(t.Context(), file.blobKey, strings.NewReader("bytes"))
	require.NoError(t, err)
	_, err = PrepareRecoveryReplay(t.Context(), source, assets, at, []RecoveryChange{deletion})
	require.Error(t, err)
	require.NoError(t, assets.Retire(t.Context(), file.blobKey))
	changes, err := PrepareRecoveryReplay(t.Context(), source, assets, at, []RecoveryChange{deletion})
	require.NoError(t, err)
	require.Nil(t, changes[0].NewValue)
	// Expired and pending records retain their original state. Recovery never makes them readable.
	file.ExpiresAt = at
	expired := recoveryFileEvidence(t, file)
	_, err = PrepareRecoveryReplay(t.Context(), fileReplaySource{}, assets, at, []RecoveryChange{{Account: file.Account, ID: file.ID, After: &expired}})
	require.NoError(t, err)
	file.State = FileStatePending
	pending := recoveryFileEvidence(t, file)
	_, err = PrepareRecoveryReplay(t.Context(), fileReplaySource{}, assets, at, []RecoveryChange{{Account: file.Account, ID: file.ID, After: &pending}})
	require.NoError(t, err)
}

func TestFileRecoveryRefusesUnsupportedSchemaAndTransientRecord(t *testing.T) {
	file := sampleFile("owner", "file")
	record := recoveryFileEvidence(t, file)
	key := storageKey(file.Account, file.ID)
	bad := append(append([]byte(nil), record.state.raw[:len(record.state.raw)-1]...), []byte(`,"future_private_fact":"retained"}`)...)
	var unsupported RecoveryFile
	require.Error(t, unsupported.UnmarshalJSON(bad))
	_, err := CaptureRecoveryFile(t.Context(), fileReplaySource{key: {Key: key, Value: record.state.raw, ExpiresAtMillis: 1}}, file.Account, file.ID)
	require.Error(t, err)
}
