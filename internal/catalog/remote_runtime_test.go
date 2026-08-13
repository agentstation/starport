package catalog

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/catalogs/evidence"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

func TestGenerationStoresKeepRemoteAndAcceptedPointersIndependent(t *testing.T) {
	store := storage.NewMockStore()
	accepted, err := NewGenerationStore(store)
	require.NoError(t, err)
	remote, err := newRemoteGenerationStore(store)
	require.NoError(t, err)
	first := runtimeTestGeneration(t, "remote-first", testEmptyCatalog(t, "first"), time.Now().UTC())
	second := runtimeTestGeneration(
		t,
		"remote-second",
		testEmptyCatalog(t, "second"),
		first.Manifest.GeneratedAt.Add(time.Minute),
	)

	require.NoError(t, remote.Commit(t.Context(), first, ""))
	_, err = accepted.Current(t.Context())
	require.ErrorIs(t, err, starmaperrors.ErrNotFound)
	require.NoError(t, accepted.Commit(t.Context(), first, ""))
	require.NoError(t, remote.Commit(t.Context(), second, first.Manifest.GenerationID))

	acceptedCurrent, err := accepted.Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, first.Manifest.GenerationID, acceptedCurrent.Manifest.GenerationID)
	remoteCurrent, err := remote.Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, second.Manifest.GenerationID, remoteCurrent.Manifest.GenerationID)
	shared, err := accepted.Get(t.Context(), second.Manifest.GenerationID)
	require.NoError(t, err)
	require.Equal(t, second.Manifest.Payload.Checksum, shared.Manifest.Payload.Checksum)
}

func TestRemoteRuntimeAcceptsOnlyMatchingForwardState(t *testing.T) {
	store := storage.NewMockStore()
	accepted, err := NewGenerationStore(store)
	require.NoError(t, err)
	remoteStore, err := newRemoteGenerationStore(store)
	require.NoError(t, err)
	first := runtimeTestGeneration(t, "accept-first", testEmptyCatalog(t, "first"), time.Now().UTC())
	second := runtimeTestGeneration(
		t,
		"accept-second",
		testEmptyCatalog(t, "second"),
		first.Manifest.GeneratedAt.Add(time.Minute),
	)
	require.NoError(t, accepted.Commit(t.Context(), first, ""))
	require.NoError(t, remoteStore.Commit(t.Context(), first, ""))
	require.NoError(t, remoteStore.Commit(t.Context(), second, first.Manifest.GenerationID))
	client, err := starmap.New()
	require.NoError(t, err)
	control, err := Open(client)
	require.NoError(t, err)
	runtime := newRemoteRuntime(
		&runtimeTestSubscriber{},
		remoteStore,
		accepted,
		control,
		time.Millisecond,
	)

	secondState := runtimeTestState(t, second)
	require.NoError(t, runtime.Accept(t.Context(), secondState))
	current, err := accepted.Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, second.Manifest.GenerationID, current.Manifest.GenerationID)

	staleErr := runtime.Accept(t.Context(), runtimeTestState(t, first))
	require.Error(t, staleErr)
	mismatch := secondState
	mismatch.PayloadChecksum = "different"
	require.Error(t, runtime.Accept(t.Context(), mismatch))
}

func TestRemoteRuntimeEmitsEachAtomicStateOnceAndJoins(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	control, err := Open(client)
	require.NoError(t, err)
	store := storage.NewMockStore()
	accepted, err := NewGenerationStore(store)
	require.NoError(t, err)
	remoteStore, err := newRemoteGenerationStore(store)
	require.NoError(t, err)
	subscriber := &runtimeTestSubscriber{state: client.CurrentCatalogState()}
	runtime := newRemoteRuntime(
		subscriber,
		remoteStore,
		accepted,
		control,
		time.Millisecond,
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	require.NoError(t, runtime.Start(ctx))
	next := subscriber.State()
	next.GenerationID = "observed-next"
	next.PayloadChecksum = "observed-checksum"
	subscriber.setState(next)
	select {
	case got := <-runtime.Updates():
		require.Equal(t, next.GenerationID, got.GenerationID)
	case <-time.After(time.Second):
		t.Fatal("remote runtime did not emit the changed atomic state")
	}
	select {
	case duplicate := <-runtime.Updates():
		t.Fatalf("duplicate state emitted: %#v", duplicate)
	case <-time.After(10 * time.Millisecond):
	}

	require.NoError(t, runtime.Close(t.Context()))
	require.NoError(t, ctx.Err())
	require.Equal(t, 1, subscriber.closeCalls)
}

func TestRemoteCatalogAPIKeyTransportClonesRequest(t *testing.T) {
	base := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, "catalog-secret", request.Header.Get(remoteAPIKeyHeader))
		_, hasDeadline := request.Context().Deadline()
		require.True(t, hasDeadline)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    request,
		}, nil
	})
	client := remoteHTTPClient(
		&http.Client{Transport: base, Timeout: time.Hour},
		"catalog-secret",
		time.Second,
	)
	require.Zero(t, client.Timeout)
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"http://127.0.0.1/catalog",
		nil,
	)
	require.NoError(t, err)
	response, err := client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Empty(t, request.Header.Get(remoteAPIKeyHeader))
}

func TestRemoteCatalogEventStreamUsesLivenessInsteadOfFetchDeadline(t *testing.T) {
	base := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		_, hasDeadline := request.Context().Deadline()
		require.False(t, hasDeadline)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})
	client := remoteHTTPClient(&http.Client{Transport: base}, "", time.Millisecond)
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"http://127.0.0.1/updates/stream",
		nil,
	)
	require.NoError(t, err)
	request.Header.Set("Accept", remoteEventStreamMediaType)
	response, err := client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
}

type runtimeTestSubscriber struct {
	mu         sync.RWMutex
	state      starmap.CatalogState
	closeCalls int
}

func (s *runtimeTestSubscriber) Start(context.Context) error { return nil }

func (s *runtimeTestSubscriber) Close() error {
	s.mu.Lock()
	s.closeCalls++
	s.mu.Unlock()
	return nil
}

func (s *runtimeTestSubscriber) State() starmap.CatalogState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *runtimeTestSubscriber) setState(state starmap.CatalogState) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func testEmptyCatalog(t testing.TB, providerID catalogs.ProviderID) *catalogs.Catalog {
	t.Helper()
	builder := catalogs.NewEmpty()
	require.NoError(t, builder.SetProvider(catalogs.Provider{
		ID: providerID, Name: string(providerID),
	}))
	result, err := builder.Build()
	require.NoError(t, err)
	return result
}

func runtimeTestGeneration(
	t testing.TB,
	generationID string,
	catalog *catalogs.Catalog,
	generatedAt time.Time,
) catalogs.Generation {
	t.Helper()
	payload, err := catalogs.EncodeCatalogPayload(catalog)
	require.NoError(t, err)
	descriptor := catalogs.DescribeCatalogPayload(payload)
	generation := catalogs.Generation{
		Manifest: catalogs.GenerationManifest{
			ManifestVersion: catalogs.CurrentGenerationManifestVersion,
			SchemaVersion:   catalogs.CurrentCatalogSchemaVersion,
			GenerationID:    generationID,
			GeneratedAt:     generatedAt,
			Payload:         descriptor,
			Validation: catalogs.GenerationValidationReport{
				ValidatorVersion: "starport-remote-runtime-test/v1",
				ValidatedAt:      generatedAt,
				Status:           catalogs.GenerationValidationPassed,
				Checks: []catalogs.GenerationValidationCheck{{
					Name: "test", Status: catalogs.GenerationValidationCheckPassed,
				}},
			},
			SyncRunID: "sync-" + generationID,
			SourceObservations: []catalogs.SourceObservationLink{{
				Source:        evidence.LocalCatalogID,
				ObservationID: "observation-" + generationID,
				ObservedAt:    generatedAt,
				Revision: evidence.ObservationRevision{
					Kind:  evidence.ObservationRevisionKindContentDigest,
					Value: descriptor.Checksum,
				},
				Completeness:     evidence.ObservationCompletenessComplete,
				Status:           evidence.ObservationStatusSucceeded,
				EvidenceChecksum: descriptor.Checksum,
			}},
			ReviewCandidates: []evidence.ReviewCandidate{},
			Completeness:     catalogs.GenerationCompletenessComplete,
			ConsumerCompatibility: catalogs.ConsumerCompatibility{
				MinSchemaVersion: catalogs.CurrentCatalogSchemaVersion,
				MaxSchemaVersion: catalogs.CurrentCatalogSchemaVersion,
			},
		},
		Payload: payload,
	}
	require.NoError(t, generation.Validate())
	return generation
}

func runtimeTestState(t testing.TB, generation catalogs.Generation) starmap.CatalogState {
	t.Helper()
	catalog, err := catalogs.DecodeCatalogPayload(generation.Payload)
	require.NoError(t, err)
	return starmap.CatalogState{
		Catalog: catalog, GenerationID: generation.Manifest.GenerationID,
		PayloadChecksum: generation.Manifest.Payload.Checksum,
		GeneratedAt:     generation.Manifest.GeneratedAt,
		Sequence:        1,
	}
}
