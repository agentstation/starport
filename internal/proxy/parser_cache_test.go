package proxy

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starmap"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/document"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
)

// PLG-V12 and PLG-V13 at the proxy. The tests in internal/document state what
// the cache holds and what scopes an entry. These state the thing an operator
// pays for: a document a conversation resends is read once, and the turn says
// whether it paid to read its attachments.
//
// A recognition call is the unit of cost here, so every test counts calls
// rather than inspecting the store. A cache that held the right entries and
// still called the recognizer would be a cache that cost money.

// extractionStore is the byte store the proxy cache tests run over. It holds
// values in memory and ignores the window, because the window belongs to the
// tests in internal/document.
type extractionStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newExtractionStore() *extractionStore {
	return &extractionStore{values: make(map[string][]byte)}
}

func (s *extractionStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, found := s.values[key]
	return value, found, nil
}

func (s *extractionStore) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

// catalogContext returns a context carrying a runtime lease, which is where the
// parser reads the catalog generation its keys are scoped to. A turn without
// one is the subject of its own test below.
func catalogContext(t *testing.T) context.Context {
	t.Helper()
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	return connectors.ContextWithRuntimeLease(context.Background(),
		&cacheRuntimeLease{snapshot: plane.Current()})
}

// cachingProxy returns a proxy whose document reads are cached, and the router
// that answers its recognition calls.
func cachingProxy(t *testing.T, page string) (*proxy, *recognizingRouter) {
	t.Helper()
	cache, err := document.NewCache(newExtractionStore(), nil, 0)
	require.NoError(t, err)
	router := newRecognizingRouter(page)
	return &proxy{router: router, extractions: cache}, router
}

// TestTheSameDocumentIsRecognizedOnceAcrossTurns holds the acceptance PLG5
// exists for.
//
// A conversation about one document resends that document on every turn, and a
// retry sends it again. Without the cache each turn pays the recognizer for
// pages that already read the same way, and the second answer is the first
// answer at twice the price.
func TestTheSameDocumentIsRecognizedOnceAcrossTurns(t *testing.T) {
	t.Parallel()
	service, router := cachingProxy(t, "INVOICE 4471\nAmount due: $912.00")
	ctx := catalogContext(t)

	first, err := service.ProcessChatCompletion(ctx,
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)
	require.Equal(t, 1, router.calls)
	require.False(t, first.ExtractionCached,
		"the first read of a document was reported as free")

	second, err := service.ProcessChatCompletion(ctx,
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)
	require.Equal(t, 1, router.calls,
		"the second turn paid to read a document it had already read")
	require.True(t, second.ExtractionCached)

	require.Contains(t, sentText(t, router.req), "INVOICE 4471",
		"the cached turn sent the model something other than the document")
}

// TestACachedReadCrossesNoAccount states the boundary that makes the cache safe
// to run at all. Two tenants can hold the same contract, and one tenant's read
// is not the other tenant's to collect: the second account pays for its own.
func TestACachedReadCrossesNoAccount(t *testing.T) {
	t.Parallel()
	service, router := cachingProxy(t, "recognized page")
	ctx := catalogContext(t)

	_, err := service.ProcessChatCompletion(ctx,
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)

	other := parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition)
	other.TenantID = "tenant-b"
	response, err := service.ProcessChatCompletion(ctx, other)
	require.NoError(t, err)

	require.Equal(t, 2, router.calls,
		"one account read a document another account paid to read")
	require.False(t, response.ExtractionCached)
	require.Equal(t, "tenant-b", router.asked.TenantID)
}

// TestANativeReadIsCachedWithoutCallingARecognizer states that the cheap engine
// uses the same entry shape as the paid one. A native read costs no provider
// call, so this asserts the saving that remains: the turn reports the read as
// cached, and the recognizer stays out of it either way.
func TestANativeReadIsCachedWithoutCallingARecognizer(t *testing.T) {
	t.Parallel()
	service, router := cachingProxy(t, "never read")
	ctx := catalogContext(t)

	first, err := service.ProcessChatCompletion(ctx,
		parsedRequest(t, "handwritten.pdf", inference.ParserEngineNative))
	require.NoError(t, err)
	require.False(t, first.ExtractionCached)

	second, err := service.ProcessChatCompletion(ctx,
		parsedRequest(t, "handwritten.pdf", inference.ParserEngineNative))
	require.NoError(t, err)
	require.True(t, second.ExtractionCached)
	require.Zero(t, router.calls, "a natively read document reached a provider")
	require.Contains(t, sentText(t, router.req), "Starport native extraction proof")
}

// TestOneEngineNeverServesAnother holds the quietest defect the key prevents.
//
// The native engine reads a scanned page as nothing, and the recognition engine
// reads it as its contents. An entry that ignored which engine wrote it would
// hand the model an empty document and no error, and the model would answer
// from a page it never saw.
func TestOneEngineNeverServesAnother(t *testing.T) {
	t.Parallel()
	service, router := cachingProxy(t, "INVOICE 4471")
	ctx := catalogContext(t)

	_, err := service.ProcessChatCompletion(ctx,
		parsedRequest(t, "scanned.pdf", inference.ParserEngineNative))
	require.NoError(t, err)
	require.Zero(t, router.calls)

	response, err := service.ProcessChatCompletion(ctx,
		parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
	require.NoError(t, err)

	require.Equal(t, 1, router.calls,
		"a native read of a scanned page was served to the recognition engine")
	require.False(t, response.ExtractionCached)
	require.Contains(t, sentText(t, router.req), "INVOICE 4471")
}

// TestATurnWithoutACatalogGenerationStillReadsItsDocument states how the cache
// fails.
//
// The generation scopes every entry, so a turn that cannot name one has no key
// to read or write under. It reads the document instead of refusing it. A
// gateway whose document turns stop working when its cache does is worse than
// one that reads the same page twice.
func TestATurnWithoutACatalogGenerationStillReadsItsDocument(t *testing.T) {
	t.Parallel()
	service, router := cachingProxy(t, "INVOICE 4471")

	for turn := 1; turn <= 2; turn++ {
		response, err := service.ProcessChatCompletion(context.Background(),
			parsedRequest(t, "scanned.pdf", inference.ParserEngineRecognition))
		require.NoError(t, err)
		require.False(t, response.ExtractionCached)
		require.Equal(t, turn, router.calls)
		require.Contains(t, sentText(t, router.req), "INVOICE 4471")
	}
}
