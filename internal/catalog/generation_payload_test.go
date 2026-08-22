package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

// badgerInMemoryValueLimit is the per-value ceiling Badger enforces only when
// it runs in memory. The development runtime always runs in memory, so a
// generation store that writes the whole encoded generation as one value fails
// there for every real catalog.
const badgerInMemoryValueLimit = 1 << 20

// bulkTestModels is sized so the encoded generation clears the in-memory value
// limit several times over, as a real Starmap catalog does.
const bulkTestModels = 900

// largeTestGeneration builds a generation whose encoded form exceeds the
// in-memory value limit by a wide margin, matching the size a real Starmap
// catalog reaches.
func largeTestGeneration(t testing.TB, generationID string) catalogs.Generation {
	t.Helper()
	builder := catalogs.NewEmpty()
	require.NoError(t, builder.SetProvider(catalogs.Provider{
		ID: "bulk-provider", Name: "Bulk Provider",
	}))
	require.NoError(t, builder.SetAuthor(catalogs.Author{ID: "bulk", Name: "Bulk"}))
	// Descriptions are the bulk of a real catalog's payload, so the filler
	// sits where the real bytes sit rather than in identifiers.
	description := strings.Repeat("catalog payload filler. ", 64)
	textOnly := func() *catalogs.ModelFeatures {
		return &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{
			Input:  []catalogs.ModelModality{catalogs.ModelModalityText},
			Output: []catalogs.ModelModality{catalogs.ModelModalityText},
		}}
	}
	for index := range bulkTestModels {
		slug := fmt.Sprintf("bulk-model-%04d", index)
		require.NoError(t, builder.SetAuthorModel("bulk", catalogs.Model{
			ID: slug, Name: fmt.Sprintf("Bulk Model %04d", index),
			Description: description,
			Authors:     []catalogs.Author{{ID: "bulk", Name: "Bulk"}},
			Features:    textOnly(),
		}))
		require.NoError(t, builder.SetProviderModel("bulk-provider", catalogs.Model{
			ID: slug, ModelRef: catalogs.ModelDefinitionID("bulk/" + slug),
			Name:        fmt.Sprintf("Bulk Model %04d", index),
			Description: description,
			Status:      catalogs.ModelStatusActive,
			Features:    textOnly(),
		}))
	}
	source, err := builder.Build()
	require.NoError(t, err)
	generation := runtimeTestGeneration(t, generationID, source, time.Now().UTC())

	// Without this the test could go vacuous: a smaller catalog would fit in
	// one value and stop covering the defect it exists to catch.
	encoded, err := json.Marshal(generation)
	require.NoError(t, err)
	require.Greater(
		t, len(encoded), badgerInMemoryValueLimit,
		"the test generation must exceed the in-memory value limit to cover the defect",
	)
	return generation
}

func openInMemoryBadger(t testing.TB) storage.KVStore {
	t.Helper()
	store, err := storage.OpenBadger(storage.BadgerConfig{
		InMemory: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

// TestCommitAcceptsGenerationLargerThanInMemoryValueLimit is the regression
// test for the development-runtime catalog refresh. Before the generation
// payload was chunked, this commit failed with "Value with size N exceeded
// 1048576 limit" and every `starport dev` refresh returned HTTP 500.
func TestCommitAcceptsGenerationLargerThanInMemoryValueLimit(t *testing.T) {
	generation := largeTestGeneration(t, "large-generation")
	store := openInMemoryBadger(t)
	generationStore, err := NewGenerationStore(store)
	require.NoError(t, err)

	require.NoError(t, generationStore.Commit(t.Context(), generation, ""))

	loaded, err := generationStore.Current(t.Context())
	require.NoError(t, err)
	require.Equal(t, generation.Manifest.GenerationID, loaded.Manifest.GenerationID)

	catalog, err := catalogs.DecodeCatalogPayload(loaded.Payload)
	require.NoError(t, err)
	offerings, err := catalog.ProviderOfferings("bulk-provider")
	require.NoError(t, err)
	require.Len(t, offerings, bulkTestModels)
}

// TestGenerationStoreWritesNoOversizedValue proves the invariant the fix rests
// on: whatever the catalog size, every value the generation store writes stays
// under the smallest limit any supported backend imposes.
func TestGenerationStoreWritesNoOversizedValue(t *testing.T) {
	generation := largeTestGeneration(t, "sized-generation")
	store := storage.NewMockStore()
	generationStore, err := NewGenerationStore(store)
	require.NoError(t, err)
	require.NoError(t, generationStore.Commit(t.Context(), generation, ""))

	keys, err := store.Scan(t.Context(), "*", 0)
	require.NoError(t, err)
	require.NotEmpty(t, keys)

	chunks := 0
	for _, key := range keys {
		value, err := store.Get(t.Context(), key)
		require.NoError(t, err)
		require.LessOrEqual(
			t, len(value), generationChunkSize,
			"key %q holds %d bytes, over the %d-byte chunk size",
			key, len(value), generationChunkSize,
		)
		require.Less(t, len(value), badgerInMemoryValueLimit)
		if strings.HasPrefix(key, catalogGenerationChunkKeyPrefix) {
			chunks++
		}
	}
	require.Greater(t, chunks, 1, "a payload over the value limit must span chunks")
}

// TestCommitIsIdempotentForIdenticalContent locks in that re-committing the
// same generation succeeds. Chunks are content-addressed, so the retry writes
// the same keys and the record comparison sees the same digest.
func TestCommitIsIdempotentForIdenticalContent(t *testing.T) {
	generation := largeTestGeneration(t, "repeat-generation")
	generationStore, err := NewGenerationStore(openInMemoryBadger(t))
	require.NoError(t, err)

	require.NoError(t, generationStore.Commit(t.Context(), generation, ""))
	require.NoError(t, generationStore.Commit(t.Context(), generation, ""))

	loaded, err := generationStore.Get(t.Context(), generation.Manifest.GenerationID)
	require.NoError(t, err)
	require.Equal(t, generation.Manifest.GenerationID, loaded.Manifest.GenerationID)
}

// TestGetReportsMissingChunk proves a truncated store fails with a cause a
// reader can act on rather than a decode error about malformed JSON.
func TestGetReportsMissingChunk(t *testing.T) {
	generation := largeTestGeneration(t, "truncated-generation")
	store := storage.NewMockStore()
	generationStore, err := NewGenerationStore(store)
	require.NoError(t, err)
	require.NoError(t, generationStore.Commit(t.Context(), generation, ""))

	keys, err := store.Scan(t.Context(), "*", 0)
	require.NoError(t, err)
	removed := false
	for _, key := range keys {
		if strings.HasPrefix(key, catalogGenerationChunkKeyPrefix) {
			require.NoError(t, store.Delete(t.Context(), key))
			removed = true
			break
		}
	}
	require.True(t, removed, "commit stored no chunk to remove")

	_, err = generationStore.Get(t.Context(), generation.Manifest.GenerationID)
	require.ErrorContains(t, err, "is missing chunk")
}

// TestEncodeGenerationPayloadRoundTrip covers the codec directly, including
// the empty and exactly-one-chunk boundaries.
func TestEncodeGenerationPayloadRoundTrip(t *testing.T) {
	for name, size := range map[string]int{
		"empty":            0,
		"single byte":      1,
		"exact chunk":      generationChunkSize,
		"chunk plus one":   generationChunkSize + 1,
		"several chunks":   generationChunkSize*3 + 17,
		"repeated content": generationChunkSize * 2,
	} {
		t.Run(name, func(t *testing.T) {
			payload := make([]byte, size)
			for index := range payload {
				// "repeated content" leaves the payload all zeroes, so both
				// chunks hash alike and the reader must handle a repeated
				// digest.
				if name != "repeated content" {
					payload[index] = byte(index % 251)
				}
			}
			record, chunks := encodeGenerationPayload(payload)
			require.Equal(t, size, record.Size)
			require.Equal(t, generationEncodingChunked, record.Encoding)

			store := storage.NewMockStore()
			require.NoError(t, writeGenerationChunks(t.Context(), store, chunks))
			loaded, err := readGenerationPayload(t.Context(), store, record, "test")
			require.NoError(t, err)
			require.Equal(t, payload, loaded)
		})
	}
}

// TestDecodeGenerationRecordRejectsForeignValue proves a store written before
// the chunked encoding fails by name instead of decoding as an empty payload.
func TestDecodeGenerationRecordRejectsForeignValue(t *testing.T) {
	_, err := decodeGenerationRecord([]byte(`{"manifest":{"generation_id":"old"}}`), "old")
	require.ErrorContains(t, err, "declares no encoding")
}
