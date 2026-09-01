package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/agentstation/starport/internal/inference"
)

// The semantic cache is the opt-in similarity layer beside the exact
// identity. It never owns a response: it holds vectors that point at exact
// entries, inside a similarity scope that pins everything but the prompt
// text. Two requests share a scope exactly when a paraphrase is the only
// thing that separates them — same account, catalog generation, model,
// sampling, tools, and routing policy — so a similarity answer can never
// cross a boundary the exact identity would have kept apart.

// SemanticIndexSchemaVersion identifies the stored vector-index encoding.
const SemanticIndexSchemaVersion = 1

const (
	// DefaultSemanticThreshold is the minimum cosine similarity that
	// answers when the deployment does not configure one. It is set high:
	// a wrong answer served confidently costs more than a provider call.
	DefaultSemanticThreshold = 0.95
	// DefaultSemanticMaxEntries bounds the vectors one similarity scope
	// holds when the deployment does not configure a bound. The scope
	// embeds the account, so this is also the per-account scan bound.
	DefaultSemanticMaxEntries = 128
)

var (
	// ErrNoPromptText reports a request with no text to embed. A purely
	// non-text request has no paraphrase, so the layer has nothing to do.
	ErrNoPromptText = errors.New("semantic cache needs prompt text")
)

// SemanticScope derives the similarity scope for one eligible chat request:
// the exact identity with every text part blanked, plus the prompt text
// those parts held. The scope key hashes everything the exact key hashes
// except the text, so entries under one scope differ only in wording.
func SemanticScope(identity ChatIdentity) (scopeKey, promptText string, err error) {
	identity.Request = identity.Request.Clone()
	var prompt strings.Builder
	hasText := false
	for messageIndex := range identity.Request.Messages {
		message := &identity.Request.Messages[messageIndex]
		for partIndex := range message.Content {
			part := &message.Content[partIndex]
			if part.Kind != inference.ContentText {
				continue
			}
			if strings.TrimSpace(part.Text) != "" {
				hasText = true
			}
			if prompt.Len() > 0 {
				prompt.WriteString("\n")
			}
			prompt.WriteString(string(message.Role))
			prompt.WriteString(": ")
			prompt.WriteString(part.Text)
			part.Text = ""
		}
	}
	if !hasText {
		return "", "", fmt.Errorf("%w: %w", ErrIneligible, ErrNoPromptText)
	}
	promptText = prompt.String()
	payload, err := buildChatPayload(identity)
	if err != nil {
		return "", "", err
	}
	scopeKey, err = semanticKey("semantic_cache", payload)
	if err != nil {
		return "", "", err
	}
	return scopeKey, promptText, nil
}

// Cosine returns the cosine similarity of two vectors, or 0 when the two
// cannot be compared: different lengths, empty, or zero magnitude.
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / math.Sqrt(normA*normB)
}

// SemanticMatch is one similarity answer: the exact entry it points at and
// the cosine similarity that cleared the threshold.
type SemanticMatch struct {
	Key        string
	Similarity float64
}

// SemanticIndex holds prompt vectors per similarity scope and answers the
// nearest entry above the threshold.
type SemanticIndex interface {
	// Lookup answers the best entry whose cosine similarity to the vector
	// clears the threshold, or reports no match.
	Lookup(ctx context.Context, scopeKey string, vector []float32) (SemanticMatch, bool, error)
	// Add records a vector pointing at one exact entry, bounded per scope:
	// the oldest vector leaves when the scope is full.
	Add(ctx context.Context, scopeKey string, vector []float32, exactKey string) error
	// Drop removes the vector pointing at one exact entry. The caller uses
	// it when the entry has left the store, so the vector goes with it.
	Drop(ctx context.Context, scopeKey, exactKey string) error
}

type semanticIndex struct {
	store      Store
	threshold  float64
	maxEntries int
}

// OpenSemanticIndex creates a vector index over the response-cache store.
// A zero threshold or bound takes the built-in default.
func OpenSemanticIndex(store Store, threshold float64, maxEntries int) (SemanticIndex, error) {
	if store == nil {
		return nil, ErrStoreRequired
	}
	if threshold <= 0 {
		threshold = DefaultSemanticThreshold
	}
	if threshold > 1 {
		return nil, fmt.Errorf("semantic cache threshold %v is above 1", threshold)
	}
	if maxEntries <= 0 {
		maxEntries = DefaultSemanticMaxEntries
	}
	return &semanticIndex{store: store, threshold: threshold, maxEntries: maxEntries}, nil
}

type semanticIndexRecord struct {
	SchemaVersion int                  `json:"schema_version"`
	ScopeKey      string               `json:"scope_key"`
	Entries       []semanticIndexEntry `json:"entries"`
}

type semanticIndexEntry struct {
	Key    string    `json:"key"`
	Vector []float32 `json:"vector"`
}

func (i *semanticIndex) Lookup(ctx context.Context, scopeKey string, vector []float32) (SemanticMatch, bool, error) {
	stored, found, err := i.load(ctx, scopeKey)
	if err != nil || !found {
		return SemanticMatch{}, false, err
	}
	best := SemanticMatch{}
	for _, entry := range stored.Entries {
		similarity := Cosine(vector, entry.Vector)
		if similarity > best.Similarity {
			best = SemanticMatch{Key: entry.Key, Similarity: similarity}
		}
	}
	if best.Key == "" || best.Similarity < i.threshold {
		return SemanticMatch{}, false, nil
	}
	return best, true, nil
}

func (i *semanticIndex) Add(ctx context.Context, scopeKey string, vector []float32, exactKey string) error {
	if scopeKey == "" || exactKey == "" || len(vector) == 0 {
		return ErrCorruptRecord
	}
	stored, _, err := i.load(ctx, scopeKey)
	if err != nil {
		return err
	}
	entries := withoutEntry(stored.Entries, exactKey)
	entries = append(entries, semanticIndexEntry{Key: exactKey, Vector: append([]float32(nil), vector...)})
	if len(entries) > i.maxEntries {
		entries = entries[len(entries)-i.maxEntries:]
	}
	return i.save(ctx, scopeKey, entries)
}

func (i *semanticIndex) Drop(ctx context.Context, scopeKey, exactKey string) error {
	stored, found, err := i.load(ctx, scopeKey)
	if err != nil || !found {
		return err
	}
	entries := withoutEntry(stored.Entries, exactKey)
	if len(entries) == len(stored.Entries) {
		return nil
	}
	return i.save(ctx, scopeKey, entries)
}

func withoutEntry(entries []semanticIndexEntry, exactKey string) []semanticIndexEntry {
	kept := make([]semanticIndexEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Key != exactKey {
			kept = append(kept, entry)
		}
	}
	return kept
}

func (i *semanticIndex) load(ctx context.Context, scopeKey string) (semanticIndexRecord, bool, error) {
	data, found, err := i.store.GetResponse(ctx, scopeKey)
	if err != nil || !found {
		return semanticIndexRecord{}, found, err
	}
	var stored semanticIndexRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		return semanticIndexRecord{}, false, fmt.Errorf("%w: decode: %v", ErrCorruptRecord, err)
	}
	if stored.SchemaVersion != SemanticIndexSchemaVersion || stored.ScopeKey != scopeKey {
		return semanticIndexRecord{}, false, ErrCorruptRecord
	}
	return stored, true, nil
}

func (i *semanticIndex) save(ctx context.Context, scopeKey string, entries []semanticIndexEntry) error {
	data, err := json.Marshal(semanticIndexRecord{
		SchemaVersion: SemanticIndexSchemaVersion,
		ScopeKey:      scopeKey,
		Entries:       entries,
	})
	if err != nil {
		return fmt.Errorf("encode semantic index record: %w", err)
	}
	return i.store.SetResponse(ctx, scopeKey, data)
}
