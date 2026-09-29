package storage

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	// TransferBarrierKey blocks ordinary startup until deployment recovery completes.
	TransferBarrierKey = "storage:import:v1"
	// TransferMaxKeyBytes is the portable Badger key limit.
	TransferMaxKeyBytes = 65000
	// TransferMaxValueBytes bounds one transfer allocation.
	TransferMaxValueBytes = 64 << 20
	transferMaxExpiry     = int64(253402300799999)
)

// ErrImportRestricted refuses startup until the deployment recovery procedure completes.
var ErrImportRestricted = errors.New("storage import requires deployment recovery before startup")

// TransferRecord preserves bytes and absolute expiry. Zero expiry means persistent.
type TransferRecord struct {
	Key             string
	Value           []byte
	ExpiresAtMillis int64
}

// Validate checks portable representation without changing a record.
func (r TransferRecord) Validate() error {
	if len(r.Key) == 0 || len(r.Key) > TransferMaxKeyBytes || r.Key == TransferBarrierKey || strings.HasPrefix(r.Key, "!badger!") {
		return ErrInvalidKey
	}
	if len(r.Value) > TransferMaxValueBytes {
		return ErrValueTooLarge
	}
	if r.ExpiresAtMillis < 0 || r.ExpiresAtMillis > transferMaxExpiry {
		return ErrInvalidMutation
	}
	return nil
}

// RecordSource enumerates durable records without exposing writes.
type RecordSource interface {
	Enumerate(context.Context, func(TransferRecord) error) error
}

// RecordTransfer serves a stopped deployment's backup and import coordinator.
// The caller must fence source and target writers throughout the operation.
// Enumerate can repeat identical records. Import leaves a persistent barrier.
// These operations do not grant permission to start a recovered deployment.
type RecordTransfer interface {
	RecordSource
	ExpiryResolution() time.Duration
	Claim(context.Context, []byte) error
	Import(context.Context, []byte, TransferRecord) error
}

// OpenRecordTransfer binds a shared operation to an explicitly selected process.
// Observing that process does not prove external fencing or recovery approval.
func OpenRecordTransfer(ctx context.Context, store KVStore, identity string) (RecordTransfer, error) {
	switch s := store.(type) {
	case *BadgerStore:
		if identity != "" {
			return nil, ErrIncarnationChanged
		}
		return &badgerTransfer{store: s}, nil
	case *ValkeyStore:
		bound, err := s.BindIncarnation(ctx, identity)
		if err != nil {
			return nil, err
		}
		return &valkeyTransfer{bound: bound.(*valkeyIncarnationStore)}, nil
	default:
		return nil, errors.New("record transfer requires a native durable backend")
	}
}

func validateTransferClaim(claim []byte) error {
	if len(claim) == 0 || len(claim) > 4096 {
		return ErrInvalidMutation
	}
	return nil
}

// CheckImportBarrier refuses a partial or unapproved restored deployment.
func CheckImportBarrier(ctx context.Context, store KVStore) error {
	found, err := store.Exists(ctx, TransferBarrierKey)
	if err != nil {
		return err
	}
	if found {
		return ErrImportRestricted
	}
	return nil
}
