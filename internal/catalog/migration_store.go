package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"

	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/storage"
)

type migrationStoreReceipt struct {
	Version         int                               `json:"version"`
	Request         runtime.DirectoryMigrationRequest `json:"request"`
	GenerationID    string                            `json:"generation_id"`
	PayloadChecksum string                            `json:"payload_checksum"`
}

func (m RuntimeMigration) storeReceiptKey(settings Settings) string {
	owner := settings.directoryOwner()
	key := sha256.Sum256([]byte(owner.Product + "\x00" + owner.Deployment + "\x00" + owner.Instance + "\x00" + m.OperationID))
	return "catalog_migration:v1:" + hex.EncodeToString(key[:])
}

func (m RuntimeMigration) bindStore(ctx context.Context, store storage.KVStore, settings Settings) error {
	if store == nil {
		return fmt.Errorf("runtime migration requires the original catalog store")
	}
	key := m.storeReceiptKey(settings)
	if _, err := store.Get(ctx, key); err == nil {
		return m.verifyStore(ctx, store, settings)
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	accepted, err := NewGenerationStore(store)
	if err != nil {
		return err
	}
	receipt := migrationStoreReceipt{Version: 1, Request: m.request(settings)}
	current, err := accepted.Current(ctx)
	if err != nil && !errors.Is(err, starmaperrors.ErrNotFound) {
		return err
	}
	if err == nil {
		receipt.GenerationID = current.Manifest.GenerationID
		receipt.PayloadChecksum = current.Manifest.Payload.Checksum
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err := store.CompareAndSwap(ctx, key, nil, encoded); err != nil {
		// A concurrent retry can install the same operation before this writer.
		if verifyErr := m.verifyStore(ctx, store, settings); verifyErr != nil {
			return errors.Join(err, verifyErr)
		}
	}
	return m.verifyStore(ctx, store, settings)
}

func (m RuntimeMigration) verifyStore(ctx context.Context, store storage.KVStore, settings Settings) error {
	if store == nil {
		return fmt.Errorf("runtime migration requires the original catalog store")
	}
	raw, err := store.Get(ctx, m.storeReceiptKey(settings))
	if err != nil {
		return fmt.Errorf("read runtime migration catalog binding: %w", err)
	}
	var receipt migrationStoreReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return fmt.Errorf("decode runtime migration catalog binding: %w", err)
	}
	if receipt.Version != 1 || receipt.Request != m.request(settings) {
		return fmt.Errorf("runtime migration catalog binding differs from the selected operation")
	}
	accepted, err := NewGenerationStore(store)
	if err != nil {
		return err
	}
	if receipt.GenerationID == "" {
		if receipt.PayloadChecksum != "" {
			return fmt.Errorf("runtime migration has an incomplete catalog binding")
		}
		// A cold deployment can still use its embedded baseline without an accepted head.
		_, err := accepted.Current(ctx)
		if errors.Is(err, starmaperrors.ErrNotFound) {
			return nil
		}
		return err
	}
	original, err := accepted.Get(ctx, receipt.GenerationID)
	if err != nil {
		return fmt.Errorf("read original accepted migration catalog: %w", err)
	}
	if receipt.PayloadChecksum == "" || original.Manifest.Payload.Checksum != receipt.PayloadChecksum {
		return fmt.Errorf("runtime migration catalog checksum differs from its binding")
	}
	current, err := accepted.Current(ctx)
	if err != nil {
		return fmt.Errorf("read retained migration catalog: %w", err)
	}
	return validateAcceptedOrder(original.Manifest, current.Manifest)
}
