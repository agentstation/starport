package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dgraph-io/badger/v4"
)

func TestReadOnlyStoreRejectsEveryWriteContract(t *testing.T) {
	ctx := context.Background()
	store := &readOnlyStore{KVStore: NewMockStore()}
	writes := []func() error{
		func() error { return store.Set(ctx, "key", []byte("value")) },
		func() error { return store.Delete(ctx, "key") },
		func() error { return store.SetWithTTL(ctx, "key", []byte("value"), time.Minute) },
		func() error { return store.ExpireAt(ctx, "key", time.Now()) },
		func() error { _, err := store.Increment(ctx, "key", 1); return err },
		func() error { _, err := store.Decrement(ctx, "key", 1); return err },
		func() error { return store.CompareAndSwap(ctx, "key", nil, []byte("value")) },
		func() error { return store.CompareAndSwapBatch(ctx, []CompareAndSwapMutation{{Key: "key"}}) },
		func() error { return store.BatchSet(ctx, map[string][]byte{"key": []byte("value")}) },
		func() error { return store.BatchDelete(ctx, []string{"key"}) },
		func() error {
			return store.BatchSetWithTTL(ctx, map[string][]byte{"key": []byte("value")}, time.Minute)
		},
		func() error { _, err := store.BeginTransaction(ctx); return err },
	}
	for index, write := range writes {
		if err := write(); !errors.Is(err, ErrReadOnly) {
			t.Errorf("write %d error = %v, want %v", index, err, ErrReadOnly)
		}
	}
	if err := store.Ping(ctx); err != nil {
		t.Errorf("Ping() error = %v", err)
	}
}

func TestOpenBadgerReadOnlyDoesNotChangeStoredValues(t *testing.T) {
	ctx := context.Background()
	configuration := BadgerConfig{
		Path: t.TempDir(), Compression: true, NumVersions: 1,
		NumLevelZero: 5, MemTableSize: 64 << 20,
	}
	writable, err := OpenBadger(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := writable.Set(ctx, "key", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := writable.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenReadOnly(Config{Type: StorageTypeBadger, Badger: configuration})
	if errors.Is(err, ErrReadOnlyUnsupported) {
		t.Skipf("Badger read-only inspection is unavailable: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Get(ctx, "key")
	if err != nil || string(value) != "value" {
		t.Fatalf("Get() = %q, %v", value, err)
	}
	if err := store.Set(ctx, "key", []byte("changed")); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Set() error = %v, want %v", err, ErrReadOnly)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenBadgerReadOnly(configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	value, err = reopened.Get(ctx, "key")
	if err != nil || string(value) != "value" {
		t.Fatalf("reopened Get() = %q, %v", value, err)
	}
}

func TestBadgerReadOnlyRecoveryErrorIsClassified(t *testing.T) {
	err := badgerOpenError(true, badger.ErrTruncateNeeded)
	if !errors.Is(err, ErrReadOnlyRecoveryRequired) {
		t.Errorf("error = %v, want %v", err, ErrReadOnlyRecoveryRequired)
	}
	if !errors.Is(err, badger.ErrTruncateNeeded) {
		t.Errorf("error = %v, want Badger cause", err)
	}

	err = badgerOpenError(false, badger.ErrTruncateNeeded)
	if errors.Is(err, ErrReadOnlyRecoveryRequired) {
		t.Errorf("writable error was classified as read-only recovery: %v", err)
	}
}

func TestBadgerUnsupportedReadOnlyErrorIsClassified(t *testing.T) {
	for _, cause := range []error{badger.ErrWindowsNotSupported, badger.ErrPlan9NotSupported} {
		err := badgerOpenError(true, cause)
		if !errors.Is(err, ErrReadOnlyUnsupported) {
			t.Errorf("error = %v, want %v", err, ErrReadOnlyUnsupported)
		}
		if !errors.Is(err, cause) {
			t.Errorf("error = %v, want cause %v", err, cause)
		}
	}
}
