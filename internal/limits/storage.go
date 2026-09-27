package limits

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

const (
	// StoredBytesSchemaVersion identifies durable byte claims and their aggregate.
	StoredBytesSchemaVersion = 2
	// StoredBytesPrefix is the durable byte accounting namespace.
	StoredBytesPrefix    = "limits:v2:stored_bytes:"
	storageClaimAttempts = 16
)

var (
	ErrStorageFull           = errors.New("stored bytes limit exceeded")
	ErrStorageHistoryUnknown = errors.New("stored byte history requires recovery")
	ErrStorageClaimConflict  = errors.New("stored byte claim conflicts with retained identity")
	ErrStorageClaimReleased  = errors.New("stored byte claim is closed")
)

type byteTotal struct {
	Version int   `json:"version"`
	Bytes   int64 `json:"bytes"`
}
type byteClaim struct {
	Version   int       `json:"version"`
	Holder    string    `json:"holder"`
	ID        string    `json:"id"`
	Initial   int64     `json:"initial"`
	Bytes     int64     `json:"bytes"`
	Bound     int64     `json:"bound"`
	Measured  bool      `json:"measured"`
	Attached  bool      `json:"attached"`
	Released  bool      `json:"released"`
	CreatedAt time.Time `json:"created_at"`
}

// StorageMeter owns durable file claims and their atomic account total.
type StorageMeter struct {
	store           storage.KVStore
	recoveryMu      sync.Mutex
	recoveryCursor  string
	recoveryNext    string
	recoveryPending []string
	recoveryLoaded  bool
}

func NewStorageMeter(store storage.KVStore) (*StorageMeter, error) {
	if store == nil {
		return nil, ErrCounterRequired
	}
	return &StorageMeter{store: store}, nil
}

// InitializeEmpty requires the file owner to prove that the account has no files.
// Existing legacy or claim evidence prevents an implicit reset to zero.
func (m *StorageMeter) InitializeEmpty(ctx context.Context, holder string) error {
	if strings.TrimSpace(holder) == "" {
		return ErrInvalidHolder
	}
	if _, _, err := m.readTotal(ctx, holder); err == nil {
		return nil
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if _, err := m.store.Get(ctx, "limits:v1:stored_bytes:"+holder); err == nil {
		return ErrStorageHistoryUnknown
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	claims, err := m.store.ScanWithPrefix(ctx, byteClaimPrefix(holder), 1)
	if err != nil {
		return err
	}
	if len(claims) > 0 {
		return ErrStorageHistoryUnknown
	}
	data, _ := json.Marshal(byteTotal{Version: StoredBytesSchemaVersion})
	err = m.store.CompareAndSwap(ctx, byteTotalKey(holder), nil, data)
	if errors.Is(err, storage.ErrConflict) {
		_, _, err = m.readTotal(ctx, holder)
	}
	return err
}

// Reserve records one file claim before bytes or file metadata can publish.
func (m *StorageMeter) Reserve(ctx context.Context, holder, id string, size, bound int64) error {
	if strings.TrimSpace(holder) == "" {
		return ErrInvalidHolder
	}
	if id == "" || size < 0 || bound < 0 {
		return ErrStorageClaimConflict
	}
	for range storageClaimAttempts {
		held, oldClaim, err := m.readClaim(ctx, holder, id)
		if err == nil {
			if held.Released {
				return ErrStorageClaimReleased
			}
			if held.Initial != size || held.Bound != bound {
				return ErrStorageClaimConflict
			}
			total, _, err := m.readTotal(ctx, holder)
			if err != nil {
				return err
			}
			if total.Bytes < held.Bytes {
				_, current, err := m.readClaim(ctx, holder, id)
				if err != nil {
					return err
				}
				if !bytes.Equal(current, oldClaim) {
					continue
				}
				return ErrStorageHistoryUnknown
			}
			return nil
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		total, old, err := m.readTotal(ctx, holder)
		if err != nil {
			return err
		}
		if size > math.MaxInt64-total.Bytes || bound > 0 && (total.Bytes > bound || size > bound-total.Bytes) {
			return ErrStorageFull
		}
		total.Bytes += size
		claim := byteClaim{Version: StoredBytesSchemaVersion, Holder: holder, ID: id, Initial: size, Bytes: size, Bound: bound, CreatedAt: time.Now().UTC()}
		err = m.writeBytes(ctx, holder, claim, nil, total, old)
		if !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return storage.ErrConflict
}

// Resize sets the measured claim size once. Exact retries preserve its total.
func (m *StorageMeter) Resize(ctx context.Context, holder, id string, size int64) error {
	if size < 0 {
		return ErrStorageClaimConflict
	}
	for range storageClaimAttempts {
		claim, oldClaim, err := m.readClaim(ctx, holder, id)
		if err != nil {
			return err
		}
		if claim.Released {
			return ErrStorageClaimReleased
		}
		if !claim.Attached {
			return ErrStorageClaimConflict
		}
		total, oldTotal, err := m.readTotal(ctx, holder)
		if err != nil {
			return err
		}
		if total.Bytes < claim.Bytes {
			_, current, readErr := m.readClaim(ctx, holder, id)
			if readErr != nil {
				return readErr
			}
			if !bytes.Equal(current, oldClaim) {
				continue
			}
			return ErrStorageHistoryUnknown
		}
		if claim.Measured {
			if claim.Bytes != size {
				return ErrStorageClaimConflict
			}
			return nil
		}
		other := total.Bytes - claim.Bytes
		if size > math.MaxInt64-other || claim.Bound > 0 && (other > claim.Bound || size > claim.Bound-other) {
			return ErrStorageFull
		}
		claim.Bytes = size
		claim.Measured = true
		total.Bytes = other + size
		err = m.writeBytes(ctx, holder, claim, oldClaim, total, oldTotal)
		if !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return storage.ErrConflict
}

// Attachment joins a reserved byte claim to the file metadata write.
func (m *StorageMeter) Attachment(ctx context.Context, holder, id string) (storage.CompareAndSwapMutation, error) {
	claim, old, err := m.readClaim(ctx, holder, id)
	if err != nil {
		return storage.CompareAndSwapMutation{}, err
	}
	if claim.Released {
		return storage.CompareAndSwapMutation{}, ErrStorageClaimReleased
	}
	if claim.Attached {
		return storage.CompareAndSwapMutation{}, ErrStorageClaimConflict
	}
	claim.Attached = true
	data, _ := json.Marshal(claim)
	return storage.CompareAndSwapMutation{Key: byteClaimKey(holder, id), ExpectedValue: old, NewValue: data}, nil
}

// Release closes a claim after its bytes disappear. Retried cleanup releases nothing twice.
func (m *StorageMeter) Release(ctx context.Context, holder, id string) error {
	return m.releaseClaim(ctx, holder, id, false)
}

// Abort fences an unattached preparation without releasing a published file.
func (m *StorageMeter) Abort(ctx context.Context, holder, id string) error {
	return m.releaseClaim(ctx, holder, id, true)
}

func (m *StorageMeter) releaseClaim(ctx context.Context, holder, id string, unattached bool) error {
	for range storageClaimAttempts {
		claim, oldClaim, err := m.readClaim(ctx, holder, id)
		if err != nil {
			return err
		}
		if claim.Released {
			return nil
		}
		if unattached && claim.Attached {
			return ErrStorageClaimConflict
		}
		total, oldTotal, err := m.readTotal(ctx, holder)
		if err != nil {
			return err
		}
		if total.Bytes < claim.Bytes {
			// A concurrent release can change the claim after the first read.
			current, _, readErr := m.readClaim(ctx, holder, id)
			if readErr != nil {
				return readErr
			}
			if current.Released {
				return nil
			}
			if current.Bytes != claim.Bytes {
				continue
			}
			return ErrStorageHistoryUnknown
		}
		total.Bytes -= claim.Bytes
		claim.Released = true
		err = m.writeBytes(ctx, holder, claim, oldClaim, total, oldTotal)
		if !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return storage.ErrConflict
}

// Total refuses missing accounting history instead of reporting a free account.
func (m *StorageMeter) Total(ctx context.Context, holder string) (int64, error) {
	total, _, err := m.readTotal(ctx, holder)
	return total.Bytes, err
}
func (m *StorageMeter) readTotal(ctx context.Context, holder string) (byteTotal, []byte, error) {
	if strings.TrimSpace(holder) == "" {
		return byteTotal{}, nil, ErrInvalidHolder
	}
	data, err := m.store.GetBounded(ctx, byteTotalKey(holder), 1024)
	if err != nil {
		return byteTotal{}, nil, err
	}
	var total byteTotal
	if json.Unmarshal(data, &total) != nil || total.Version != StoredBytesSchemaVersion || total.Bytes < 0 {
		return byteTotal{}, nil, ErrStorageHistoryUnknown
	}
	return total, data, nil
}
func (m *StorageMeter) readClaim(ctx context.Context, holder, id string) (byteClaim, []byte, error) {
	if strings.TrimSpace(holder) == "" {
		return byteClaim{}, nil, ErrInvalidHolder
	}
	data, err := m.store.GetBounded(ctx, byteClaimKey(holder, id), 8192)
	if err != nil {
		return byteClaim{}, nil, err
	}
	var claim byteClaim
	if json.Unmarshal(data, &claim) != nil || claim.Version != StoredBytesSchemaVersion || claim.Holder != holder || claim.ID != id || id == "" || claim.Initial < 0 || claim.Bytes < 0 || claim.Bound < 0 || claim.CreatedAt.IsZero() {
		return byteClaim{}, nil, ErrStorageHistoryUnknown
	}
	return claim, data, nil
}
func (m *StorageMeter) writeBytes(ctx context.Context, holder string, claim byteClaim, oldClaim []byte, total byteTotal, oldTotal []byte) error {
	c, _ := json.Marshal(claim)
	t, _ := json.Marshal(total)
	return m.store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{{Key: byteClaimKey(holder, claim.ID), ExpectedValue: oldClaim, NewValue: c}, {Key: byteTotalKey(holder), ExpectedValue: oldTotal, NewValue: t}})
}
func byteTotalKey(holder string) string {
	return StoredBytesPrefix + base64.RawURLEncoding.EncodeToString([]byte(holder)) + ":total"
}
func byteClaimPrefix(holder string) string {
	return StoredBytesPrefix + base64.RawURLEncoding.EncodeToString([]byte(holder)) + ":claim:"
}
func byteClaimKey(holder, id string) string {
	return byteClaimPrefix(holder) + base64.RawURLEncoding.EncodeToString([]byte(id))
}
