package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
)

// LocalRecoveryTarget binds an actual durable Badger handle to its selected native directory.
// It grants no activation, budget authority, or current admission permission.
type LocalRecoveryTarget struct{ state *localRecoveryTargetState }
type localRecoveryTargetState struct {
	store                  *BadgerStore
	directory              *productfiles.Directory
	path, identity, digest string
}

// Format excludes private target paths and retained identities from diagnostics.
func (LocalRecoveryTarget) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private local recovery target>"))
}

// OpenLocalRecoveryTarget checks an actual writable, persistent, synchronized Badger handle.
// expectedSHA256 must come from the independently accepted target decision.
// This function reads native scope without creating files, repairing state, or observing a remote incarnation.
func OpenLocalRecoveryTarget(ctx context.Context, store KVStore, expectedSHA256 string) (*LocalRecoveryTarget, error) {
	if ctx == nil || !transferDigest(expectedSHA256) {
		return nil, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	native, ok := store.(*BadgerStore)
	if !ok || native == nil {
		return nil, ErrConflict
	}
	native.mu.RLock()
	defer native.mu.RUnlock()
	if err := validateLocalRecoveryHandle(native); err != nil {
		return nil, err
	}
	directory, err := productfiles.ExistingDirectory(native.config.Path)
	if err != nil {
		return nil, err
	}
	identity, err := directory.Identity()
	if err != nil {
		return nil, err
	}
	actual, err := (Config{Type: StorageTypeBadger, Badger: native.config}).RecoveryTargetSHA256("")
	if err != nil || actual != expectedSHA256 {
		return nil, errors.Join(ErrConflict, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &LocalRecoveryTarget{state: &localRecoveryTargetState{store: native, directory: directory, path: native.config.Path, identity: identity, digest: actual}}, nil
}

// TargetSHA256 returns the original selected scope digest. Reads alone grant no permission.
func (t *LocalRecoveryTarget) TargetSHA256() string {
	if t == nil || t.state == nil {
		return ""
	}
	return t.state.digest
}

// Check verifies the original handle, durability settings, path, and native directory identity.
// It neither adopts a replacement directory nor changes any database state.
func (t *LocalRecoveryTarget) Check(ctx context.Context) error {
	if ctx == nil || t == nil || t.state == nil || t.state.store == nil || t.state.directory == nil {
		return ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	native := t.state.store
	native.mu.RLock()
	defer native.mu.RUnlock()
	if err := validateLocalRecoveryHandle(native); err != nil {
		return err
	}
	if native.config.Path != t.state.path || !transferDigest(t.state.digest) {
		return ErrConflict
	}
	identity, err := t.state.directory.Identity()
	if err != nil || identity != t.state.identity {
		return errors.Join(ErrConflict, err)
	}
	actual, err := (Config{Type: StorageTypeBadger, Badger: native.config}).RecoveryTargetSHA256("")
	if err != nil || actual != t.state.digest {
		return errors.Join(ErrConflict, err)
	}
	return ctx.Err()
}

// CheckActivatedImportAt reads the exact current native activation claim, cursor, and decision.
// It checks target continuity before and after the native read. It creates or repairs no records.
func (t *LocalRecoveryTarget) CheckActivatedImportAt(ctx context.Context, claim []byte, position ImportReplayPosition, decisionSHA256 string) error {
	if err := validateTransferClaim(claim); err != nil {
		return err
	}
	claim = bytes.Clone(claim)
	if err := t.Check(ctx); err != nil {
		return err
	}
	if err := (&badgerTransfer{store: t.state.store}).CheckActivatedImportAt(ctx, claim, position, decisionSHA256); err != nil {
		return err
	}
	return t.Check(ctx)
}

func validateLocalRecoveryHandle(store *BadgerStore) error {
	if store.closed || store.db == nil {
		return ErrStorageClosed
	}
	if store.readOnly || store.config.InMemory || !store.config.SyncWrites || !filepath.IsAbs(store.config.Path) || filepath.Clean(store.config.Path) != store.config.Path {
		return ErrConflict
	}
	options := store.db.Opts()
	if options.ReadOnly || options.InMemory || !options.SyncWrites || options.Dir != store.config.Path || options.ValueDir != store.config.Path {
		return ErrConflict
	}
	return nil
}
