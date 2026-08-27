package document_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/document"
)

// PLG-V12 and PLG-V13. The cache exists so identical bytes are read once. Each
// field of the key is a way that "identical" can be wrong, and the tests below
// hold one field each: the same bytes under a different account, a different
// engine, or a different catalog generation are not the same read.

// memoryStore is a byte store with a real time-to-live, because the window is
// half of what the cache promises and a store that ignored expiry would let
// every window test pass.
type memoryStore struct {
	mu      sync.Mutex
	clock   *testClock
	entries map[string]memoryEntry
	failGet error
	writes  int
}

type memoryEntry struct {
	value   []byte
	expires time.Time
}

func newMemoryStore(clock *testClock) *memoryStore {
	return &memoryStore{clock: clock, entries: map[string]memoryEntry{}}
}

func (s *memoryStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failGet != nil {
		return nil, false, s.failGet
	}
	entry, found := s.entries[key]
	if !found || !entry.expires.After(s.clock.Now()) {
		return nil, false, nil
	}
	return entry.value, true, nil
}

func (s *memoryStore) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	s.entries[key] = memoryEntry{value: value, expires: s.clock.Now().Add(ttl)}
	return nil
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func cacheFixture(t *testing.T) (*document.Cache, *memoryStore, *testClock) {
	t.Helper()
	clock := newTestClock()
	store := newMemoryStore(clock)
	cache, err := document.NewCache(store, clock, time.Hour)
	require.NoError(t, err)
	return cache, store, clock
}

func recognitionKey() document.CacheKey {
	return document.CacheKey{
		AccountID:   "tenant-a",
		ContentHash: document.ContentHash([]byte("%PDF-1.7 scanned invoice")),
		Engine:      "mistral-ocr",
		Generation:  "catalog-generation-7",
	}
}

// TestTheSameBytesAreReadOnceForOneAccountEngineAndGeneration is the whole
// point of the cache stated once: a second request carrying the same document
// gets the first request's text back.
func TestTheSameBytesAreReadOnceForOneAccountEngineAndGeneration(t *testing.T) {
	t.Parallel()
	cache, store, _ := cacheFixture(t)
	ctx := context.Background()
	key := recognitionKey()

	_, found, err := cache.Get(ctx, key)
	require.NoError(t, err)
	require.False(t, found, "an empty cache answered a read")

	require.NoError(t, cache.Put(ctx, key, document.Reading{
		Text: "Invoice 4021", Pages: 3, Offering: "mistralai/mistral-ocr",
	}))

	reading, found, err := cache.Get(ctx, key)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "Invoice 4021", reading.Text)
	require.Equal(t, 3, reading.Pages)
	// The offering is recorded rather than keyed. It says which model produced
	// the text, which an operator reads and a later request does not have to
	// predict before it can find the entry.
	require.Equal(t, "mistralai/mistral-ocr", reading.Offering)
	require.Equal(t, 1, store.writes)
}

// TestAnotherAccountGetsItsOwnEntry holds the account scope. The bytes are
// identical and the read still happens again, because one tenant's upload is
// not another tenant's to read back.
func TestAnotherAccountGetsItsOwnEntry(t *testing.T) {
	t.Parallel()
	cache, _, _ := cacheFixture(t)
	ctx := context.Background()
	paid := recognitionKey()
	require.NoError(t, cache.Put(ctx, paid, document.Reading{Text: "Invoice 4021", Pages: 3}))

	other := paid
	other.AccountID = "tenant-b"
	require.Equal(t, paid.ContentHash, other.ContentHash)

	_, found, err := cache.Get(ctx, other)
	require.NoError(t, err)
	require.False(t, found, "one account read another account's document")
}

// TestTheEngineIsPartOfTheKey holds the field that would be the quietest to
// get wrong. A scanned page reads as nothing natively and as its contents
// under recognition, so a native entry answering a recognition request would
// hand the model an empty document and charge for nothing.
func TestTheEngineIsPartOfTheKey(t *testing.T) {
	t.Parallel()
	cache, _, _ := cacheFixture(t)
	ctx := context.Background()
	native := recognitionKey()
	native.Engine = "native"
	require.NoError(t, cache.Put(ctx, native, document.Reading{Text: "", Pages: 3}))

	_, found, err := cache.Get(ctx, recognitionKey())
	require.NoError(t, err)
	require.False(t, found, "the native engine's answer was served to a recognition request")
}

// TestANewCatalogGenerationMissesTheCache holds the last key field. A
// generation is what decides which offerings serve the recognition operation
// at all, so text read under an older catalog is text a newer catalog did not
// necessarily produce.
func TestANewCatalogGenerationMissesTheCache(t *testing.T) {
	t.Parallel()
	cache, _, _ := cacheFixture(t)
	ctx := context.Background()
	require.NoError(t, cache.Put(ctx, recognitionKey(), document.Reading{Text: "Invoice 4021", Pages: 3}))

	next := recognitionKey()
	next.Generation = "catalog-generation-8"
	_, found, err := cache.Get(ctx, next)
	require.NoError(t, err)
	require.False(t, found, "a catalog change did not invalidate the entry")
}

// TestDifferentBytesAreADifferentDocument closes the set. The content hash is
// the only field that is about the document itself.
func TestDifferentBytesAreADifferentDocument(t *testing.T) {
	t.Parallel()
	cache, _, _ := cacheFixture(t)
	ctx := context.Background()
	require.NoError(t, cache.Put(ctx, recognitionKey(), document.Reading{Text: "Invoice 4021", Pages: 3}))

	other := recognitionKey()
	other.ContentHash = document.ContentHash([]byte("%PDF-1.7 a different invoice"))
	require.NotEqual(t, recognitionKey().ContentHash, other.ContentHash)

	_, found, err := cache.Get(ctx, other)
	require.NoError(t, err)
	require.False(t, found)
}

// TestAReadStopsBeingReusableAfterTheWindow holds the configured window.
//
// The cache checks the window itself rather than trusting the store, because a
// store is free to keep a value past the lifetime it was written with, and how
// long text stays reusable is the gateway's own answer rather than the store's.
func TestAReadStopsBeingReusableAfterTheWindow(t *testing.T) {
	t.Parallel()
	clock := newTestClock()
	store := newMemoryStore(clock)
	// The store is told to keep the value far longer than the cache's window,
	// so an expiry seen here is the cache's own.
	forgetful := &neverExpiringStore{memoryStore: store}
	cache, err := document.NewCache(forgetful, clock, 30*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 30*time.Minute, cache.Window())

	ctx := context.Background()
	require.NoError(t, cache.Put(ctx, recognitionKey(), document.Reading{Text: "Invoice 4021", Pages: 3}))

	clock.advance(29 * time.Minute)
	_, found, err := cache.Get(ctx, recognitionKey())
	require.NoError(t, err)
	require.True(t, found)

	clock.advance(2 * time.Minute)
	_, found, err = cache.Get(ctx, recognitionKey())
	require.NoError(t, err)
	require.False(t, found, "text stayed reusable past the configured window")
}

type neverExpiringStore struct{ *memoryStore }

func (s *neverExpiringStore) Set(ctx context.Context, key string, value []byte, _ time.Duration) error {
	return s.memoryStore.Set(ctx, key, value, 100*365*24*time.Hour)
}

// TestAnIncompleteKeyNeitherReadsNorWrites holds the fail-closed rule. A key
// missing a field is a key whose scope is unknown, and serving under one would
// cross whichever scope went missing.
func TestAnIncompleteKeyNeitherReadsNorWrites(t *testing.T) {
	t.Parallel()
	cache, store, _ := cacheFixture(t)
	ctx := context.Background()

	for name, mutate := range map[string]func(*document.CacheKey){
		"account":    func(k *document.CacheKey) { k.AccountID = "" },
		"content":    func(k *document.CacheKey) { k.ContentHash = "" },
		"engine":     func(k *document.CacheKey) { k.Engine = "" },
		"generation": func(k *document.CacheKey) { k.Generation = "" },
	} {
		key := recognitionKey()
		mutate(&key)
		_, found, err := cache.Get(ctx, key)
		require.ErrorIs(t, err, document.ErrIncompleteCacheKey, name)
		require.False(t, found, name)
		require.ErrorIs(t, cache.Put(ctx, key, document.Reading{Text: "x"}),
			document.ErrIncompleteCacheKey, name)
	}
	require.Zero(t, store.writes, "an incomplete key reached the store")
}

// TestACorruptRecordIsAMissRatherThanText holds the shape of a bad read. The
// caller's own answer to a miss is to read the document, which is right for a
// record this schema cannot trust.
func TestACorruptRecordIsAMissRatherThanText(t *testing.T) {
	t.Parallel()
	cache, store, clock := cacheFixture(t)
	ctx := context.Background()
	key := recognitionKey()
	require.NoError(t, store.Set(ctx, key.String(), []byte("{not a record"), time.Hour))

	_, found, err := cache.Get(ctx, key)
	require.ErrorIs(t, err, document.ErrCorruptCacheRecord)
	require.False(t, found)

	// A record written under another key is corrupt for the same reason: the
	// key it names is what proves the store returned the entry that was asked
	// for.
	other := recognitionKey()
	other.AccountID = "tenant-b"
	require.NoError(t, cache.Put(ctx, other, document.Reading{Text: "Invoice 4021"}))
	stolen, found, err := store.Get(ctx, other.String())
	require.NoError(t, err)
	require.True(t, found)
	require.NoError(t, store.Set(ctx, key.String(), stolen, time.Hour))
	clock.advance(0)

	_, found, err = cache.Get(ctx, key)
	require.ErrorIs(t, err, document.ErrCorruptCacheRecord)
	require.False(t, found)
}

// TestAStoreFailureIsReportedRatherThanSwallowed keeps the decision about an
// unreachable cache at the seam that owns the request. This package says what
// happened.
func TestAStoreFailureIsReportedRatherThanSwallowed(t *testing.T) {
	t.Parallel()
	cache, store, _ := cacheFixture(t)
	store.failGet = errors.New("store unreachable")

	_, found, err := cache.Get(context.Background(), recognitionKey())
	require.ErrorContains(t, err, "store unreachable")
	require.False(t, found)
}

// TestACacheNeedsAStore holds the one construction rule. A cache with no store
// would read nothing and report every read as a miss, which is a silently
// disabled cache rather than a configuration error.
func TestACacheNeedsAStore(t *testing.T) {
	t.Parallel()
	_, err := document.NewCache(nil, nil, time.Hour)
	require.ErrorIs(t, err, document.ErrCacheStoreRequired)

	cache, err := document.NewCache(newMemoryStore(newTestClock()), nil, 0)
	require.NoError(t, err)
	require.Equal(t, document.DefaultCacheWindow, cache.Window())
}

// TestTheStoreKeyRevealsNoDocument holds a property the key's shape carries
// rather than states. The fields are strings a caller influences, and a key
// built by joining them would let one field's value spell another field's
// separator and read an entry it does not name.
func TestTheStoreKeyRevealsNoDocument(t *testing.T) {
	t.Parallel()
	key := recognitionKey()
	stored := key.String()
	require.Contains(t, stored, "documentcache:v1:")
	require.NotContains(t, stored, key.AccountID)
	require.NotContains(t, stored, key.Engine)
	require.NotContains(t, stored, key.Generation)

	confusable := document.CacheKey{
		AccountID:   "tenant",
		ContentHash: "a:b",
		Engine:      "native",
		Generation:  "gen",
	}
	shifted := document.CacheKey{
		AccountID:   "tenant",
		ContentHash: "a",
		Engine:      "b:native",
		Generation:  "gen",
	}
	require.NotEqual(t, confusable.String(), shifted.String())
}
