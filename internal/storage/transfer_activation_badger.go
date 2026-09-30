package storage

import (
	"context"
	"errors"

	"github.com/dgraph-io/badger/v4"
)

func (b *badgerTransfer) ActivateImport(ctx context.Context, claim []byte, decisionSHA256 string) error {
	return b.activateImport(ctx, claim, nil, decisionSHA256, false)
}

func (b *badgerTransfer) ActivateImportAt(ctx context.Context, claim []byte, position ImportReplayPosition, decisionSHA256 string) error {
	return b.activateImport(ctx, claim, &position, decisionSHA256, false)
}

func (b *badgerTransfer) CheckActivatedImportAt(ctx context.Context, claim []byte, position ImportReplayPosition, decisionSHA256 string) error {
	return b.activateImport(ctx, claim, &position, decisionSHA256, true)
}

func (b *badgerTransfer) activateImport(ctx context.Context, claim []byte, position *ImportReplayPosition, decisionSHA256 string, inspect bool) error {
	if ctx == nil {
		return ErrInvalidMutation
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.store.config.InMemory || !b.store.config.SyncWrites {
		return errors.New("KV activation requires persistent Badger with sync_writes enabled")
	}
	key, receipt, err := activationReceiptAt(claim, position, decisionSHA256)
	if err != nil {
		return err
	}
	check := func(txn *badger.Txn) error {
		if position != nil {
			if _, err := activationPositionGuards(claim, *position, func(key string) ([]byte, error) {
				return readBadgerReconciliation(txn, key, 4096)
			}); err != nil {
				return err
			}
		}
		barrier, barrierErr := txn.Get([]byte(TransferBarrierKey))
		current, currentErr := txn.Get([]byte(transferActivationCurrent))
		historical, historyErr := txn.Get([]byte(key))
		for _, readErr := range []error{barrierErr, currentErr, historyErr} {
			if readErr != nil && !errors.Is(readErr, badger.ErrKeyNotFound) {
				return readErr
			}
		}
		if errors.Is(barrierErr, badger.ErrKeyNotFound) {
			if currentErr != nil || historyErr != nil {
				return ErrConflict
			}
			if err := matchingBadgerImport(current, receipt); err != nil {
				return err
			}
			return matchingBadgerImport(historical, receipt)
		}
		if inspect {
			return ErrConflict
		}
		if err := matchingBadgerImport(barrier, claim); err != nil {
			return err
		}
		// Imported history cannot activate its former claim or replace a decision.
		if currentErr == nil || historyErr == nil {
			return ErrConflict
		}
		if err := txn.Set([]byte(key), receipt); err != nil {
			return err
		}
		if err := txn.Set([]byte(transferActivationCurrent), receipt); err != nil {
			return err
		}
		return txn.Delete([]byte(TransferBarrierKey))
	}
	if inspect {
		return b.inspectActivation(ctx, check)
	}
	return b.update(ctx, check)
}

var _ ImportActivator = (*badgerTransfer)(nil)
var _ ImportReplayActivator = (*badgerTransfer)(nil)

var _ ImportActivationInspector = (*badgerTransfer)(nil)

func (b *badgerTransfer) inspectActivation(ctx context.Context, check func(*badger.Txn) error) error {
	b.store.mu.RLock()
	defer b.store.mu.RUnlock()
	if b.store.closed {
		return ErrStorageClosed
	}
	return b.store.db.View(func(txn *badger.Txn) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := check(txn); err != nil {
			return err
		}
		return ctx.Err()
	})
}
