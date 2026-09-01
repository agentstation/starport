package cache

import (
	"context"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/stretchr/testify/require"
)

func semanticTestIdentity(text string) ChatIdentity {
	return ChatIdentity{
		AccountID:         "account-1",
		CatalogGeneration: "catalog-1",
		Request: inference.ChatRequest{
			Model: "openai/gpt-4.1",
			Messages: []inference.Message{{
				Role:    inference.RoleUser,
				Content: []inference.ContentPart{{Kind: inference.ContentText, Text: text}},
			}},
		},
	}
}

func TestSemanticScopeSharedByParaphrasesAlone(t *testing.T) {
	first := semanticTestIdentity("What is the capital of France?")
	second := semanticTestIdentity("Tell me the capital city of France.")

	firstScope, firstText, err := SemanticScope(first)
	require.NoError(t, err)
	secondScope, secondText, err := SemanticScope(second)
	require.NoError(t, err)

	// A paraphrase shares the scope while its exact keys differ, so the
	// similarity lookup can only ever compare interchangeable requests.
	require.Equal(t, firstScope, secondScope)
	require.NotEqual(t, firstText, secondText)
	require.Contains(t, firstText, "capital of France")

	firstKey, err := ChatKey(first)
	require.NoError(t, err)
	secondKey, err := ChatKey(second)
	require.NoError(t, err)
	require.NotEqual(t, firstKey, secondKey)
	require.NotEqual(t, firstScope, firstKey)

	// Anything past the text splits the scope: a different account, model,
	// or sampling answer must never serve across the boundary.
	variants := map[string]func(*ChatIdentity){
		"account":     func(value *ChatIdentity) { value.AccountID = "account-2" },
		"generation":  func(value *ChatIdentity) { value.CatalogGeneration = "catalog-2" },
		"model":       func(value *ChatIdentity) { value.Request.Model = "openai/gpt-4.2" },
		"temperature": func(value *ChatIdentity) { value.Request.Sampling.Temperature = float32Pointer(0.2) },
		"policy":      func(value *ChatIdentity) { value.Policy.Provider.Only = []string{"openai"} },
	}
	for name, mutate := range variants {
		variant := semanticTestIdentity("What is the capital of France?")
		mutate(&variant)
		scope, _, err := SemanticScope(variant)
		require.NoError(t, err, name)
		require.NotEqual(t, firstScope, scope, name)
	}
}

func TestSemanticScopeNeedsPromptText(t *testing.T) {
	identity := semanticTestIdentity("")
	identity.Request.Messages[0].Content[0].Text = ""
	_, _, err := SemanticScope(identity)
	require.ErrorIs(t, err, ErrNoPromptText)
}

func TestCosineSimilarity(t *testing.T) {
	require.InDelta(t, 1.0, Cosine([]float32{1, 2, 3}, []float32{1, 2, 3}), 1e-9)
	require.InDelta(t, 0.0, Cosine([]float32{1, 0}, []float32{0, 1}), 1e-9)
	require.InDelta(t, -1.0, Cosine([]float32{1, 0}, []float32{-1, 0}), 1e-9)
	// Vectors that cannot be compared answer zero similarity, never a hit.
	require.Zero(t, Cosine([]float32{1, 2}, []float32{1, 2, 3}))
	require.Zero(t, Cosine(nil, nil))
	require.Zero(t, Cosine([]float32{0, 0}, []float32{1, 1}))
}

func TestSemanticIndexAnswersOnlyAboveThreshold(t *testing.T) {
	store := newMemoryStore()
	index, err := OpenSemanticIndex(store, 0.9, 8)
	require.NoError(t, err)
	ctx := context.Background()

	require.NoError(t, index.Add(ctx, "scope-1", []float32{1, 0, 0}, "entry-a"))

	// A near-identical vector clears the 0.9 threshold and names its match
	// with the cosine similarity it answered under.
	match, found, err := index.Lookup(ctx, "scope-1", []float32{0.99, 0.05, 0})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "entry-a", match.Key)
	require.GreaterOrEqual(t, match.Similarity, 0.9)

	// A distant vector stays below the threshold: no similarity answer.
	_, found, err = index.Lookup(ctx, "scope-1", []float32{0, 1, 0})
	require.NoError(t, err)
	require.False(t, found)

	// Another scope holds nothing, so nothing answers.
	_, found, err = index.Lookup(ctx, "scope-2", []float32{1, 0, 0})
	require.NoError(t, err)
	require.False(t, found)
}

func TestSemanticIndexAnswersTheNearestEntry(t *testing.T) {
	store := newMemoryStore()
	index, err := OpenSemanticIndex(store, 0.5, 8)
	require.NoError(t, err)
	ctx := context.Background()

	require.NoError(t, index.Add(ctx, "scope-1", []float32{1, 0}, "entry-a"))
	require.NoError(t, index.Add(ctx, "scope-1", []float32{0.8, 0.6}, "entry-b"))

	match, found, err := index.Lookup(ctx, "scope-1", []float32{0.9, 0.43})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "entry-b", match.Key)
}

func TestSemanticIndexBoundsEntriesPerScope(t *testing.T) {
	store := newMemoryStore()
	index, err := OpenSemanticIndex(store, 0.99, 2)
	require.NoError(t, err)
	ctx := context.Background()

	require.NoError(t, index.Add(ctx, "scope-1", []float32{1, 0, 0}, "entry-a"))
	require.NoError(t, index.Add(ctx, "scope-1", []float32{0, 1, 0}, "entry-b"))
	require.NoError(t, index.Add(ctx, "scope-1", []float32{0, 0, 1}, "entry-c"))

	// The oldest vector left when the bound was reached.
	_, found, err := index.Lookup(ctx, "scope-1", []float32{1, 0, 0})
	require.NoError(t, err)
	require.False(t, found)
	match, found, err := index.Lookup(ctx, "scope-1", []float32{0, 0, 1})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "entry-c", match.Key)
}

func TestSemanticIndexDropEvictsAVector(t *testing.T) {
	store := newMemoryStore()
	index, err := OpenSemanticIndex(store, 0.9, 8)
	require.NoError(t, err)
	ctx := context.Background()

	require.NoError(t, index.Add(ctx, "scope-1", []float32{1, 0}, "entry-a"))
	require.NoError(t, index.Drop(ctx, "scope-1", "entry-a"))

	_, found, err := index.Lookup(ctx, "scope-1", []float32{1, 0})
	require.NoError(t, err)
	require.False(t, found)

	// Dropping from an empty or absent scope is a quiet no-op.
	require.NoError(t, index.Drop(ctx, "scope-1", "entry-a"))
	require.NoError(t, index.Drop(ctx, "scope-2", "entry-a"))
}

func TestSemanticIndexRefusesBadInputs(t *testing.T) {
	_, err := OpenSemanticIndex(nil, 0, 0)
	require.ErrorIs(t, err, ErrStoreRequired)
	_, err = OpenSemanticIndex(newMemoryStore(), 1.5, 0)
	require.Error(t, err)

	index, err := OpenSemanticIndex(newMemoryStore(), 0, 0)
	require.NoError(t, err)
	ctx := context.Background()
	require.Error(t, index.Add(ctx, "", []float32{1}, "entry-a"))
	require.Error(t, index.Add(ctx, "scope-1", nil, "entry-a"))
	require.Error(t, index.Add(ctx, "scope-1", []float32{1}, ""))
}
