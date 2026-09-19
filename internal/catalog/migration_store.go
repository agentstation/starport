package catalog

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"strings"

	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/storage"
)

type migrationStoreReceipt struct {
	StoreID         string                            `json:"store_id"`
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

func (m RuntimeMigration) bindingDirectory(settings Settings, create bool) (*productfiles.Directory, error) {
	open := productfiles.ExistingDirectory
	if create {
		open = productfiles.NewDirectory
	}
	root, err := open(m.JournalRoot)
	if err != nil {
		return nil, err
	}
	child := root.ExistingChild
	if create {
		child = root.Child
	}
	host, err := child("starport-runtime")
	if err != nil {
		return nil, err
	}
	child = host.ExistingChild
	if create {
		child = host.Child
	}
	return child(strings.TrimPrefix(m.storeReceiptKey(settings), "catalog_migration:v1:"))
}

const migrationStoreIdentityKey = "catalog_migration:v1:store"

func migrationStoreIdentity(ctx context.Context, store storage.KVStore, create bool) (string, error) {
	value, err := store.Get(ctx, migrationStoreIdentityKey)
	if errors.Is(err, storage.ErrNotFound) && create {
		if err := store.CompareAndSwap(ctx, migrationStoreIdentityKey, nil, []byte(rand.Text())); err != nil {
			if !errors.Is(err, storage.ErrConflict) {
				return "", err
			}
			value, readErr := store.Get(ctx, migrationStoreIdentityKey)
			if readErr != nil {
				return "", errors.Join(err, readErr)
			}
			if len(value) == 0 {
				return "", fmt.Errorf("runtime migration store identity is empty")
			}
			return string(value), nil
		}
		value, err = store.Get(ctx, migrationStoreIdentityKey)
	}
	if err != nil {
		return "", err
	}
	if len(value) == 0 {
		return "", fmt.Errorf("runtime migration store identity is empty")
	}
	return string(value), nil
}

func (m RuntimeMigration) bindStore(ctx context.Context, store storage.KVStore, settings Settings) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil {
		return fmt.Errorf("runtime migration requires the original catalog store")
	}
	directory, err := m.bindingDirectory(settings, true)
	if err != nil {
		return err
	}
	if err := directory.RecoverPublications(ctx); err != nil {
		return err
	}
	key := m.storeReceiptKey(settings)
	encoded, err := directory.ReadFile("catalog-binding.json", 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		encoded, err = store.Get(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			identity, err := migrationStoreIdentity(ctx, store, true)
			if err != nil {
				return err
			}
			accepted, err := NewGenerationStore(store)
			if err != nil {
				return err
			}
			receipt := migrationStoreReceipt{StoreID: identity, Version: 1, Request: m.request(settings)}
			current, err := accepted.Current(ctx)
			if err != nil && !errors.Is(err, starmaperrors.ErrNotFound) {
				return err
			}
			if err == nil {
				receipt.GenerationID = current.Manifest.GenerationID
				receipt.PayloadChecksum = current.Manifest.Payload.Checksum
			}
			encoded, err = json.Marshal(receipt)
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := m.verifyStoreReceipt(ctx, store, settings, encoded); err != nil {
			return err
		}
		if err := directory.CompareAndPublish(ctx, "catalog-binding.json", nil, encoded); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := m.verifyStoreReceipt(ctx, store, settings, encoded); err != nil {
		return err
	}
	if err := store.CompareAndSwap(ctx, key, nil, encoded); err != nil {
		if !errors.Is(err, storage.ErrConflict) {
			return err
		}
		retained, readErr := store.Get(ctx, key)
		if readErr != nil {
			return errors.Join(err, readErr)
		}
		if !bytes.Equal(retained, encoded) {
			return fmt.Errorf("runtime migration catalog checkpoint differs from the host journal")
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
	directory, err := m.bindingDirectory(settings, false)
	if err != nil {
		return err
	}
	journal, err := directory.ReadFile("catalog-binding.json", 1<<20)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, journal) {
		return fmt.Errorf("runtime migration catalog store differs from the host journal")
	}
	return m.verifyStoreReceipt(ctx, store, settings, raw)
}

func (m RuntimeMigration) verifyStoreReceipt(ctx context.Context, store storage.KVStore, settings Settings, raw []byte) error {
	var receipt migrationStoreReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return fmt.Errorf("decode runtime migration catalog binding: %w", err)
	}
	if receipt.StoreID == "" || receipt.Version != 1 || receipt.Request != m.request(settings) {
		return fmt.Errorf("runtime migration catalog binding differs from the selected operation")
	}
	identity, err := migrationStoreIdentity(ctx, store, false)
	if err != nil {
		return err
	}
	if identity != receipt.StoreID {
		return fmt.Errorf("runtime migration catalog store identity differs from the host journal")
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
