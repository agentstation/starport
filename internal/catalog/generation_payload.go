package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/agentstation/starport/internal/storage"
)

const (
	catalogGenerationChunkKeyPrefix = "catalog_generation:v1:chunk:"

	// generationEncodingChunked names the generation record encoding. The
	// record stays small and constant in size; the encoded generation lives in
	// ordered content-addressed chunks, so no stored value grows with the
	// catalog.
	generationEncodingChunked = "chunked-json/1"

	// generationChunkSize bounds one stored value. Badger rejects a value over
	// 1 MiB when it runs in memory, which the development runtime always does,
	// and an encoded generation is an order of magnitude larger than that.
	generationChunkSize = 256 << 10

	// generationWriteBatchBytes bounds one chunk-write transaction, well under
	// the per-transaction budget of every supported backend.
	generationWriteBatchBytes = 2 << 20

	// generationReadBatchChunks bounds one chunk-read request.
	generationReadBatchChunks = 8
)

// generationRecord is the small immutable descriptor stored under a
// generation's key. It names the ordered content-addressed chunks that hold
// the encoded generation and the digest that proves reassembly.
type generationRecord struct {
	Encoding  string   `json:"encoding"`
	Size      int      `json:"size"`
	Digest    string   `json:"digest"`
	ChunkSize int      `json:"chunk_size"`
	Chunks    []string `json:"chunks"`
}

// encodeGenerationPayload splits an encoded generation into content-addressed
// chunks and returns the descriptor naming them. Identical content produces
// identical chunk keys, so re-committing a generation rewrites the same bytes
// and generations that share content share storage.
func encodeGenerationPayload(encoded []byte) (generationRecord, map[string][]byte) {
	record := generationRecord{
		Encoding:  generationEncodingChunked,
		Size:      len(encoded),
		Digest:    payloadDigest(encoded),
		ChunkSize: generationChunkSize,
	}
	chunks := make(map[string][]byte, len(encoded)/generationChunkSize+1)
	for start := 0; start < len(encoded); start += generationChunkSize {
		end := min(start+generationChunkSize, len(encoded))
		chunk := encoded[start:end]
		digest := payloadDigest(chunk)
		record.Chunks = append(record.Chunks, digest)
		chunks[catalogGenerationChunkKey(digest)] = chunk
	}
	return record, chunks
}

// writeGenerationChunks stores every chunk before the record that names it, so
// a committed record never points at absent content. Chunks are immutable and
// content-addressed, so an overwrite always writes identical bytes.
func writeGenerationChunks(ctx context.Context, store storage.KVStore, chunks map[string][]byte) error {
	batch := make(map[string][]byte, generationWriteBatchBytes/generationChunkSize+1)
	batched := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := store.BatchSet(ctx, batch); err != nil {
			return err
		}
		batch = make(map[string][]byte, len(batch))
		batched = 0
		return nil
	}
	for key, chunk := range chunks {
		if batched > 0 && batched+len(chunk) > generationWriteBatchBytes {
			if err := flush(); err != nil {
				return err
			}
		}
		batch[key] = chunk
		batched += len(chunk)
	}
	return flush()
}

// readGenerationPayload reassembles the encoded generation a record names and
// proves the result against the recorded digest.
func readGenerationPayload(
	ctx context.Context,
	store storage.KVStore,
	record generationRecord,
	generationID string,
) ([]byte, error) {
	if record.Encoding != generationEncodingChunked {
		return nil, fmt.Errorf(
			"catalog generation %q uses unsupported record encoding %q, want %q",
			generationID, record.Encoding, generationEncodingChunked,
		)
	}

	// One digest can repeat within a payload, so chunks are collected into a
	// digest-keyed set and the order comes from the record.
	unique := make([]string, 0, len(record.Chunks))
	seen := make(map[string]struct{}, len(record.Chunks))
	for _, digest := range record.Chunks {
		if _, ok := seen[digest]; ok {
			continue
		}
		seen[digest] = struct{}{}
		unique = append(unique, catalogGenerationChunkKey(digest))
	}

	stored := make(map[string][]byte, len(unique))
	for start := 0; start < len(unique); start += generationReadBatchChunks {
		end := min(start+generationReadBatchChunks, len(unique))
		batch, err := store.BatchGet(ctx, unique[start:end])
		if err != nil {
			return nil, fmt.Errorf("read catalog generation %q chunks: %w", generationID, err)
		}
		for key, chunk := range batch {
			stored[key] = chunk
		}
	}

	payload := make([]byte, 0, record.Size)
	for index, digest := range record.Chunks {
		chunk, ok := stored[catalogGenerationChunkKey(digest)]
		if !ok {
			return nil, fmt.Errorf(
				"catalog generation %q is missing chunk %d of %d (%s)",
				generationID, index+1, len(record.Chunks), digest,
			)
		}
		payload = append(payload, chunk...)
	}

	if len(payload) != record.Size {
		return nil, fmt.Errorf(
			"catalog generation %q reassembled %d bytes, record declares %d",
			generationID, len(payload), record.Size,
		)
	}
	if digest := payloadDigest(payload); digest != record.Digest {
		return nil, fmt.Errorf(
			"catalog generation %q reassembled to digest %s, record declares %s",
			generationID, digest, record.Digest,
		)
	}
	return payload, nil
}

// decodeGenerationRecord reads the descriptor stored under a generation key.
// A value that is not a descriptor is reported as such rather than decoded as
// a generation, so an incompatible store fails with a nameable cause.
func decodeGenerationRecord(encoded []byte, generationID string) (generationRecord, error) {
	var record generationRecord
	if err := json.Unmarshal(encoded, &record); err != nil {
		return generationRecord{}, fmt.Errorf("decode catalog generation record %q: %w", generationID, err)
	}
	if record.Encoding == "" {
		return generationRecord{}, fmt.Errorf(
			"catalog generation record %q declares no encoding; the store predates %s",
			generationID, generationEncodingChunked,
		)
	}
	return record, nil
}

// sameGenerationContent reports whether a stored record describes the content
// a commit is trying to write. Comparing digests keeps the check independent
// of record field order and avoids reading the payload back.
func sameGenerationContent(existing []byte, record generationRecord, generationID string) bool {
	stored, err := decodeGenerationRecord(existing, generationID)
	if err != nil {
		return false
	}
	return stored.Digest == record.Digest && stored.Size == record.Size
}

func catalogGenerationChunkKey(digest string) string {
	return catalogGenerationChunkKeyPrefix + digest
}

func payloadDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
