package catalog

import (
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/catalogs/evidence"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

// TestGenerationStoresKeepCandidateAndAcceptedPointersIndependent proves the
// two heads move apart. The candidate head follows the source; the accepted
// head follows route validation, and one advance never drags the other.
func TestGenerationStoresKeepCandidateAndAcceptedPointersIndependent(t *testing.T) {
	store := storage.NewMockStore()
	accepted, err := NewGenerationStore(store)
	require.NoError(t, err)
	candidates, err := newCandidateGenerationStore(store)
	require.NoError(t, err)
	first := runtimeTestGeneration(t, "candidate-first", testEmptyCatalog(t, "first"), time.Now().UTC())
	second := runtimeTestGeneration(
		t,
		"candidate-second",
		testEmptyCatalog(t, "second"),
		first.Manifest.GeneratedAt.Add(time.Minute),
	)

	require.NoError(t, candidates.Commit(t.Context(), first, ""))
	_, err = accepted.Current(t.Context())
	require.ErrorIs(t, err, starmaperrors.ErrNotFound)
	require.NoError(t, accepted.Commit(t.Context(), first, ""))
	require.NoError(t, candidates.Commit(t.Context(), second, first.Manifest.GenerationID))

	acceptedCurrent, err := accepted.Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, first.Manifest.GenerationID, acceptedCurrent.Manifest.GenerationID)
	candidateCurrent, err := candidates.Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, second.Manifest.GenerationID, candidateCurrent.Manifest.GenerationID)
	shared, err := accepted.Get(t.Context(), second.Manifest.GenerationID)
	require.NoError(t, err)
	require.Equal(t, second.Manifest.Payload.Checksum, shared.Manifest.Payload.Checksum)
}

// TestRemoteRuntimeAcceptsOnlyMatchingForwardState proves the acceptance
// transaction. It advances the accepted head for one validated candidate that
// matches its durable generation and moves the head forward, and it refuses
// every other candidate.
func TestRemoteRuntimeAcceptsOnlyMatchingForwardState(t *testing.T) {
	first := runtimeTestGeneration(t, "accept-first", testEmptyCatalog(t, "first"), time.Now().UTC())
	second := runtimeTestGeneration(
		t,
		"accept-second",
		testEmptyCatalog(t, "second"),
		first.Manifest.GeneratedAt.Add(time.Minute),
	)
	sameTime := runtimeTestGeneration(
		t,
		"accept-same-time",
		testEmptyCatalog(t, "third"),
		first.Manifest.GeneratedAt,
	)
	mismatched := runtimeTestState(t, second)
	mismatched.PayloadChecksum = "different"

	tests := []struct {
		name         string
		state        starmap.CatalogState
		wantErr      bool
		wantAccepted string
	}{
		{
			name:         "forward candidate advances the head",
			state:        runtimeTestState(t, second),
			wantAccepted: second.Manifest.GenerationID,
		},
		{
			name:         "repeated candidate writes nothing",
			state:        runtimeTestState(t, first),
			wantAccepted: first.Manifest.GenerationID,
		},
		{
			name:         "checksum mismatch is refused",
			state:        mismatched,
			wantErr:      true,
			wantAccepted: first.Manifest.GenerationID,
		},
		{
			name:         "distinct payload at one timestamp is refused",
			state:        runtimeTestState(t, sameTime),
			wantErr:      true,
			wantAccepted: first.Manifest.GenerationID,
		},
		{
			name:         "empty candidate is refused",
			state:        starmap.CatalogState{},
			wantErr:      true,
			wantAccepted: first.Manifest.GenerationID,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime, accepted := acceptanceTestRuntime(t, first, second, sameTime)
			// The head starts at the first generation, so a candidate that
			// moves backward, sideways, or nowhere is visible in the head.
			require.NoError(t, accepted.Commit(t.Context(), first, ""))

			err := runtime.Accept(t.Context(), Candidate{State: test.state})
			if test.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			current, currentErr := accepted.Current(t.Context())
			require.NoError(t, currentErr)
			require.Equal(t, test.wantAccepted, current.Manifest.GenerationID)
		})
	}
}

// TestAcceptRejectsStaleLeaseEpoch proves the lease fence. An instance that
// lost the lease during route validation carries the older epoch, and shared
// storage refuses its acceptance instead of letting two writers race.
func TestAcceptRejectsStaleLeaseEpoch(t *testing.T) {
	generation := runtimeTestGeneration(
		t, "epoch-first", testEmptyCatalog(t, "first"), time.Now().UTC(),
	)
	state := runtimeTestState(t, generation)

	tests := []struct {
		name      string
		acquire   int
		epoch     uint64
		wantStale bool
	}{
		{name: "no lease accepts every epoch", acquire: 0, epoch: 0},
		{name: "current epoch passes the fence", acquire: 1, epoch: 1},
		{name: "later epoch passes the fence", acquire: 1, epoch: 2},
		{name: "older epoch is refused", acquire: 2, epoch: 1, wantStale: true},
		{name: "absent epoch is refused", acquire: 1, epoch: 0, wantStale: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime, accepted := acceptanceTestRuntime(t, generation)
			for hold := 0; hold < test.acquire; hold++ {
				_, err := runtime.leases.AcquireLease(t.Context(), "instance", time.Minute)
				require.NoError(t, err)
			}

			err := runtime.Accept(t.Context(), Candidate{State: state, Epoch: test.epoch})
			if test.wantStale {
				require.ErrorIs(t, err, ErrStaleLeaseEpoch)
				_, currentErr := accepted.Current(t.Context())
				require.ErrorIs(t, currentErr, starmaperrors.ErrNotFound)
				return
			}
			require.NoError(t, err)
			current, currentErr := accepted.Current(t.Context())
			require.NoError(t, currentErr)
			require.Equal(t, generation.Manifest.GenerationID, current.Manifest.GenerationID)
		})
	}
}

// acceptanceTestRuntime returns a runtime whose candidate store already holds
// every named generation, together with its accepted head store.
func acceptanceTestRuntime(
	t testing.TB,
	generations ...catalogs.Generation,
) (*Runtime, *GenerationStore) {
	t.Helper()
	store := storage.NewMockStore()
	accepted, err := NewGenerationStore(store)
	require.NoError(t, err)
	candidates, err := newCandidateGenerationStore(store)
	require.NoError(t, err)
	leases, err := NewLeaseStore(store)
	require.NoError(t, err)
	previous := ""
	for _, generation := range generations {
		require.NoError(t, candidates.Commit(t.Context(), generation, previous))
		previous = generation.Manifest.GenerationID
	}
	return &Runtime{
		candidates: candidates,
		accepted:   accepted,
		leases:     leases,
		updates:    make(chan Candidate, updateBuffer),
	}, accepted
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
				ValidatorVersion: "starport-catalog-runtime-test/v1",
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
