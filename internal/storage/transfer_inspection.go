package storage

import (
	"bytes"
	"context"
	"errors"
)

// ImportReplayPosition selects the exact native replay boundary to inspect.
// Zero identifies imported state before the first replay step.
type ImportReplayPosition struct {
	Sequence      int64
	ReceiptSHA256 string
}

// ImportInspector exposes imported records without releasing their startup barrier.
// Callers must fence writers and discard all yielded records if inspection fails.
// Native control records remain private. Historical receipts remain visible.
// Enumeration can repeat identical records. The snapshot owner must deduplicate them.
type ImportInspector interface {
	InspectImport(context.Context, []byte, ImportReplayPosition, func(TransferRecord) error) error
}

func prepareImportInspection(claim []byte, position ImportReplayPosition, read func(string) ([]byte, error)) ([]CompareAndSwapMutation, error) {
	if validateTransferClaim(claim) != nil || position.Sequence < 0 || position.Sequence == 0 && position.ReceiptSHA256 != "" || position.Sequence > 0 && !transferDigest(position.ReceiptSHA256) {
		return nil, ErrInvalidMutation
	}
	var guards []CompareAndSwapMutation
	guarded := func(key string) ([]byte, error) {
		value, err := read(key)
		if err == nil {
			guards = append(guards, CompareAndSwapMutation{Key: key, ExpectedValue: bytes.Clone(value)})
		}
		return value, err
	}
	barrier, err := guarded(TransferBarrierKey)
	if err != nil || !bytes.Equal(barrier, claim) {
		return nil, errors.Join(ErrImportRestricted, err)
	}
	active, err := guarded(transferActivationCurrent)
	if err != nil || active != nil {
		return nil, errors.Join(ErrImportRestricted, err)
	}
	data, err := guarded(transferReconciliationCurrent)
	if err != nil {
		return nil, err
	}
	cursor, _, err := readReconciliationCursor(data, reconciliationDigest(claim), guarded)
	if err != nil {
		return nil, err
	}
	if cursor.Sequence != position.Sequence || cursor.ReceiptSHA256 != position.ReceiptSHA256 {
		return nil, ErrConflict
	}
	return guards, nil
}

func checkImportInspection(guards []CompareAndSwapMutation, read func(string) ([]byte, error)) error {
	for _, guard := range guards {
		data, err := read(guard.Key)
		if err != nil {
			return err
		}
		if (data == nil) != (guard.ExpectedValue == nil) || !bytes.Equal(data, guard.ExpectedValue) {
			return ErrConflict
		}
	}
	return nil
}

func importControlKey(key string) bool {
	return key == TransferBarrierKey || key == transferActivationCurrent || key == transferReconciliationCurrent
}
