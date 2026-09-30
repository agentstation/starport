package storage

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
)

// RecoveryStartupState reports native activation controls without granting admission.
// A consistent current cursor cannot substitute for the approved final position.
type RecoveryStartupState struct {
	Activated      bool
	ClaimSHA256    string
	DecisionSHA256 string
	ReceiptSHA256  string
}

// InspectRecoveryStartup checks retained native controls before application maintenance.
// The complete coordinator must separately verify its sealed decision and selected targets.
func InspectRecoveryStartup(ctx context.Context, store KVStore) (RecoveryStartupState, error) {
	if ctx == nil || store == nil {
		return RecoveryStartupState{}, ErrStorageClosed
	}
	if err := ctx.Err(); err != nil {
		return RecoveryStartupState{}, err
	}
	if err := CheckImportBarrier(ctx, store); err != nil {
		return RecoveryStartupState{}, err
	}
	read := func(key string) ([]byte, error) { return readRecoveryStartupRecord(ctx, store, key) }
	current, err := read(transferActivationCurrent)
	if err != nil {
		return RecoveryStartupState{}, err
	}
	replay, err := read(transferReconciliationCurrent)
	if err != nil {
		return RecoveryStartupState{}, err
	}
	if current == nil {
		if replay != nil {
			return RecoveryStartupState{}, ErrImportRestricted
		}
		for _, prefix := range []string{transferActivationPrefix, transferReconciliationPrefix, transferPopulatedPrefix} {
			keys, err := store.ScanWithPrefix(ctx, prefix, 1)
			if err != nil || len(keys) != 0 {
				return RecoveryStartupState{}, errors.Join(ErrImportRestricted, err)
			}
		}
		return RecoveryStartupState{}, nil
	}
	activation, err := startupActivationReceipt(current)
	if err != nil {
		return RecoveryStartupState{}, err
	}
	canonical, err := json.Marshal(activation)
	if err != nil || !bytes.Equal(canonical, current) {
		return RecoveryStartupState{}, ErrImportRestricted
	}
	history, err := read(transferActivationPrefix + activation.ClaimSHA256)
	if err != nil || !bytes.Equal(history, current) {
		return RecoveryStartupState{}, errors.Join(ErrImportRestricted, err)
	}
	cursor, _, err := readReconciliationCursor(replay, activation.ClaimSHA256, read)
	if err != nil {
		return RecoveryStartupState{}, errors.Join(ErrImportRestricted, err)
	}
	if activation.Position != nil && (cursor.Sequence != activation.Position.Sequence || cursor.ReceiptSHA256 != activation.Position.ReceiptSHA256) {
		return RecoveryStartupState{}, ErrImportRestricted
	}
	after, err := read(transferActivationCurrent)
	if err != nil || !bytes.Equal(after, current) {
		return RecoveryStartupState{}, errors.Join(ErrImportRestricted, err)
	}
	if err := CheckImportBarrier(ctx, store); err != nil {
		return RecoveryStartupState{}, err
	}
	return RecoveryStartupState{Activated: true, ClaimSHA256: activation.ClaimSHA256, DecisionSHA256: activation.DecisionSHA256, ReceiptSHA256: reconciliationDigest(current)}, nil
}

func startupActivationReceipt(current []byte) (transferActivationReceipt, error) {
	var activation transferActivationReceipt
	if json.Unmarshal(current, &activation, json.RejectUnknownMembers(true)) != nil || !validActivationReceiptPosition(activation) || len(current) > 512 ||
		!transferDigest(activation.ClaimSHA256) || !transferDigest(activation.DecisionSHA256) {
		return activation, ErrImportRestricted
	}
	return activation, nil
}

func readRecoveryStartupRecord(ctx context.Context, store KVStore, key string) ([]byte, error) {

	limit := 4096
	if key == transferActivationCurrent || strings.HasPrefix(key, transferActivationPrefix) {
		limit = 512
	}
	value, err := store.GetBounded(ctx, key, limit)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Join(ErrImportRestricted, err)
	}
	if len(value) == 0 || len(value) > limit {
		return nil, ErrImportRestricted
	}
	ttl, err := store.GetTTL(ctx, key)
	if err != nil || ttl != 0 {
		return nil, errors.Join(ErrImportRestricted, err)
	}
	return value, nil

}
