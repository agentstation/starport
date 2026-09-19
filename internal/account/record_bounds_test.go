package account

import (
	"errors"
	"github.com/agentstation/starport/internal/policyrecord"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"strings"
	"testing"
)

func TestAccountRepositoryRecordBounds(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		repository, err := Open(store)
		if err != nil {
			t.Fatal(err)
		}
		oversized := strings.Repeat("x", policyrecord.MaxBytes+1)
		if _, err := repository.Create(t.Context(), Account{ID: "large", Name: "large", Active: true, CredentialStrategy: StrategyBYOKOnly, Metadata: map[string]any{"large": oversized}}); !errors.Is(err, policyrecord.ErrTooLarge) {
			t.Fatalf("oversized write = %v", err)
		}
		if _, err := repository.GetByID(t.Context(), "large"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("refused write persisted: %v", err)
		}
		if err := store.Set(t.Context(), accountStorageKey("large"), []byte(oversized)); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.GetByID(t.Context(), "large"); !errors.Is(err, storage.ErrValueTooLarge) {
			t.Fatalf("oversized stored record = %v", err)
		}
	})
}
