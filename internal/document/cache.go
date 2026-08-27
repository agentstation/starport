package document

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A conversation resends its attachments on every turn, and a retry sends them
// again. Reading the same bytes each time is free for the native engine and
// paid per page for the recognition engine, so the cache exists for the second
// case and takes the first along with it.
//
// The cache holds text, which is a derived fact about bytes this gateway was
// handed. It never holds the bytes. Those belong to internal/blob when a
// caller stores them and to the request otherwise.

// CacheKeyVersion identifies the encoding a key was built under. Raise it when
// a field joins the key: entries a running gateway holds keep their own
// prefix, and none of them is read back under an encoding that did not write
// it.
const CacheKeyVersion = 1

// DefaultCacheWindow is how long a read stays reusable when an operator
// configures no window.
//
// A document's text does not change, so the window is not about correctness.
// It bounds how much text a gateway holds for conversations that ended, which
// is why an hour is long enough to cover a conversation and short enough that
// an idle deployment holds nothing.
const DefaultCacheWindow = time.Hour

var (
	// ErrCacheStoreRequired reports a cache built with no byte store.
	ErrCacheStoreRequired = errors.New("extraction cache store is required")
	// ErrIncompleteCacheKey reports a key missing a field that scopes it.
	// Serving an entry under a partial key would cross an account, an engine,
	// or a catalog generation, so an incomplete key never reads and never
	// writes.
	ErrIncompleteCacheKey = errors.New("extraction cache key is incomplete")
	// ErrCorruptCacheRecord reports stored data this schema cannot read.
	ErrCorruptCacheRecord = errors.New("extraction cache record is invalid")
)

// ContentHash names a document by its bytes.
//
// It is the only part of the key that is about the document itself, and it is
// what makes the cache safe: two requests share an entry when they carry the
// same bytes, whatever the caller named the file or claimed its format was.
func ContentHash(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// CacheKey names one document read.
//
// Every field scopes the entry, and dropping any one of them serves text that
// answers a different question. The account is who paid for the read. The
// content hash is which bytes were read. The engine is which reader produced
// the text, because a scanned page reads as nothing natively and as its
// contents under recognition. The generation is which catalog was in force,
// because that is what decides which offerings serve the recognition operation
// at all.
type CacheKey struct {
	// AccountID is the tenant that paid for the read. An entry never crosses
	// accounts: one tenant's upload is not another tenant's to read back.
	AccountID string
	// ContentHash is the digest of the document bytes.
	ContentHash string
	// Engine is the parser engine the caller named.
	Engine string
	// Generation is the catalog generation in force when the read ran.
	Generation string
}

// Validate reports a key that cannot scope an entry.
func (k CacheKey) Validate() error {
	missing := make([]string, 0, 4)
	for name, value := range map[string]string{
		"account":    k.AccountID,
		"content":    k.ContentHash,
		"engine":     k.Engine,
		"generation": k.Generation,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrIncompleteCacheKey, strings.Join(sorted(missing), ", "))
}

// String returns the store key this identity reads and writes under.
func (k CacheKey) String() string {
	payload := struct {
		Version     int    `json:"version"`
		AccountID   string `json:"account_id"`
		ContentHash string `json:"content_hash"`
		Engine      string `json:"engine"`
		Generation  string `json:"generation"`
	}{CacheKeyVersion, k.AccountID, k.ContentHash, k.Engine, k.Generation}
	// The fields are opaque strings a caller can influence, so they are hashed
	// rather than joined. Joining them would let one field's value spell
	// another field's separator and read an entry it does not name.
	encoded, err := json.Marshal(payload)
	if err != nil {
		// Every field is a string, so this cannot happen. Returning a key that
		// stores nothing is still better than one two identities share.
		return ""
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("documentcache:v%d:%s", CacheKeyVersion, hex.EncodeToString(digest[:]))
}

// Reading is what one engine read out of one document.
//
// It is smaller than an Extraction on purpose. A cached read answers what the
// model receives and what the account owes, and neither needs the per-page
// text layer that decided which engine ran.
type Reading struct {
	// Text is the document's text as the named engine read it.
	Text string `json:"text"`
	// Pages is how many pages the document holds.
	Pages int `json:"pages"`
	// Offering is the recognition model that produced the text. It is empty
	// when the native engine read the document, which is also how a reader
	// tells a free read from a paid one.
	Offering string `json:"offering,omitempty"`
}

// CacheStore is the byte store the cache holds entries in.
//
// It is the narrowest contract that expresses a window: a write states how
// long its value stays readable. This package names no store implementation,
// because a leaf that named one would decide where a deployment keeps its
// data.
type CacheStore interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

// CacheClock supplies the time a record is stamped and compared against.
type CacheClock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Cache holds one document's text for reuse inside a window.
type Cache struct {
	store  CacheStore
	clock  CacheClock
	window time.Duration
}

type cacheRecord struct {
	Version int     `json:"version"`
	Key     string  `json:"key"`
	ReadAt  int64   `json:"read_at"`
	Reading Reading `json:"reading"`
}

// NewCache returns a cache over the given store. An unset window takes the
// default.
func NewCache(store CacheStore, clock CacheClock, window time.Duration) (*Cache, error) {
	if store == nil {
		return nil, ErrCacheStoreRequired
	}
	if clock == nil {
		clock = systemClock{}
	}
	if window <= 0 {
		window = DefaultCacheWindow
	}
	return &Cache{store: store, clock: clock, window: window}, nil
}

// Window returns how long a read stays reusable.
func (c *Cache) Window() time.Duration { return c.window }

// Get returns the text one engine already read out of these bytes.
//
// The window is checked here as well as at the store, because a store is free
// to keep a value past the lifetime it was written with. The gateway's own
// answer to how long text stays reusable does not depend on which store a
// deployment chose.
func (c *Cache) Get(ctx context.Context, key CacheKey) (Reading, bool, error) {
	if c == nil {
		return Reading{}, false, nil
	}
	if err := key.Validate(); err != nil {
		return Reading{}, false, err
	}
	stored := key.String()
	data, found, err := c.store.Get(ctx, stored)
	if err != nil || !found {
		return Reading{}, false, err
	}
	var record cacheRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return Reading{}, false, fmt.Errorf("%w: decode: %v", ErrCorruptCacheRecord, err)
	}
	if record.Version != CacheKeyVersion || record.Key != stored || record.ReadAt == 0 {
		return Reading{}, false, ErrCorruptCacheRecord
	}
	if c.clock.Now().Sub(time.Unix(record.ReadAt, 0)) > c.window {
		return Reading{}, false, nil
	}
	return record.Reading, true, nil
}

// Put records what an engine read.
func (c *Cache) Put(ctx context.Context, key CacheKey, reading Reading) error {
	if c == nil {
		return nil
	}
	if err := key.Validate(); err != nil {
		return err
	}
	stored := key.String()
	data, err := json.Marshal(cacheRecord{
		Version: CacheKeyVersion,
		Key:     stored,
		ReadAt:  c.clock.Now().Unix(),
		Reading: reading,
	})
	if err != nil {
		return fmt.Errorf("encode extraction cache record: %w", err)
	}
	return c.store.Set(ctx, stored, data, c.window)
}

// sorted orders the names a refusal lists, so the same incomplete key reads
// the same way every time it is reported.
func sorted(values []string) []string {
	for outer := 1; outer < len(values); outer++ {
		for inner := outer; inner > 0 && values[inner] < values[inner-1]; inner-- {
			values[inner], values[inner-1] = values[inner-1], values[inner]
		}
	}
	return values
}
