package storage

import (
	"context"
	"errors"

	"github.com/dgraph-io/badger/v4"
)

func (b *badgerTransfer) ActivateImport(ctx context.Context, claim []byte, decisionSHA256 string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.store.config.InMemory || !b.store.config.SyncWrites {
		return errors.New("KV activation requires persistent Badger with sync_writes enabled")
	}
	key, receipt, err := activationReceipt(claim, decisionSHA256)
	if err != nil {
		return err
	}
	return b.update(ctx, func(txn *badger.Txn) error {
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
	})
}

var _ ImportActivator = (*badgerTransfer)(nil)
