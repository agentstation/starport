package tokenize

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tiktoken-go/tokenizer"

	"github.com/agentstation/starport/internal/inference"
)

func TestCountTextReturnsZeroForEmptyText(t *testing.T) {
	t.Parallel()
	estimator := NewEstimator()
	assert.Equal(t, 0, estimator.CountText(Hint{}, ""))
}

func TestCountTextCountsWithCodec(t *testing.T) {
	t.Parallel()
	estimator := NewEstimator()
	count := estimator.CountText(Hint{}, "Hello, world! This is a token counting test.")
	// Exact BPE counts are codec-owned; the contract is a plausible
	// nonzero count well under one token per character.
	require.Greater(t, count, 4)
	require.Less(t, count, 20)
}

func TestCountTextFallsBackWithoutCodecs(t *testing.T) {
	t.Parallel()
	estimator := &Estimator{}
	assert.Equal(t, 3, estimator.CountText(Hint{}, strings.Repeat("a", 12)))
	assert.Equal(t, 1, estimator.CountText(Hint{}, "ab"))
}

func TestCodecSelectionByHint(t *testing.T) {
	t.Parallel()
	estimator := NewEstimator()
	require.NotNil(t, estimator.codecs[tokenizer.O200kBase])
	require.NotNil(t, estimator.codecs[tokenizer.Cl100kBase])

	tests := []struct {
		name string
		hint Hint
		want tokenizer.Encoding
	}{
		{"empty hint uses modern", Hint{}, tokenizer.O200kBase},
		{"legacy gpt-4 uses cl100k", Hint{Tokenizer: "gpt", Model: "gpt-4-turbo"}, tokenizer.Cl100kBase},
		{"legacy gpt-3.5 uses cl100k", Hint{Tokenizer: "gpt", Model: "gpt-3.5-turbo"}, tokenizer.Cl100kBase},
		{"gpt-4o uses modern", Hint{Tokenizer: "gpt", Model: "gpt-4o-mini"}, tokenizer.O200kBase},
		{"gpt-4.1 uses modern", Hint{Tokenizer: "gpt", Model: "gpt-4.1"}, tokenizer.O200kBase},
		{"gpt-5 uses modern", Hint{Tokenizer: "gpt", Model: "gpt-5-nano"}, tokenizer.O200kBase},
		{"claude family approximates with modern", Hint{Tokenizer: "claude", Model: "claude-sonnet-4-5"}, tokenizer.O200kBase},
		{"llama family approximates with modern", Hint{Tokenizer: "llama3", Model: "llama-3.3-70b-versatile"}, tokenizer.O200kBase},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, estimator.codecs[test.want], estimator.codec(test.hint))
		})
	}
}

func TestCountMessagesAddsChatFraming(t *testing.T) {
	t.Parallel()
	estimator := NewEstimator()
	hint := Hint{}
	messages := []inference.Message{
		{
			Role:    inference.RoleSystem,
			Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "You are terse."}},
		},
		{
			Role:    inference.RoleUser,
			Name:    "jack",
			Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "Say hello."}},
		},
	}

	bare := estimator.CountText(hint, "You are terse.") +
		estimator.CountText(hint, "Say hello.") +
		estimator.CountText(hint, "system") +
		estimator.CountText(hint, "user") +
		estimator.CountText(hint, "jack")
	want := bare + tokensPerReply + 2*tokensPerMessage + tokensPerName
	assert.Equal(t, want, estimator.CountMessages(hint, messages))
	assert.Equal(t, 0, estimator.CountMessages(hint, nil))
}

func TestCountMessagesChargesImages(t *testing.T) {
	t.Parallel()
	estimator := NewEstimator()
	messages := []inference.Message{{
		Role: inference.RoleUser,
		Content: []inference.ContentPart{{
			Kind:  inference.ContentImage,
			Image: &inference.Image{URL: "https://example.com/cat.png"},
		}},
	}}
	withImage := estimator.CountMessages(Hint{}, messages)
	messages[0].Content[0].Image = nil
	without := estimator.CountMessages(Hint{}, messages)
	assert.Equal(t, imageTokens, withImage-without)
}

func TestCountTextIsConcurrencySafe(t *testing.T) {
	t.Parallel()
	estimator := NewEstimator()
	texts := []string{
		"The quick brown fox jumps over the lazy dog.",
		"Streaming usage normalization test corpus, line two.",
		"内部トークナイザーの同時実行テスト。",
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				for _, text := range texts {
					if estimator.CountText(Hint{}, text) <= 0 {
						t.Error("expected positive count")
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
