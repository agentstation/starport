package proxy

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/presets"
)

// staticPresetSource serves one named preset from memory, plus any
// pinned revisions the test registers.
type staticPresetSource struct {
	record    presets.Record
	revisions map[uint64]presets.Record
	err       error
}

func (s staticPresetSource) Get(_ context.Context, name string) (presets.Record, error) {
	if s.err != nil {
		return presets.Record{}, s.err
	}
	if s.record.Preset.Name != name {
		return presets.Record{}, presets.ErrNotFound
	}
	return s.record, nil
}

func (s staticPresetSource) GetRevision(_ context.Context, name string, revision uint64) (presets.Record, error) {
	if s.err != nil {
		return presets.Record{}, s.err
	}
	pinned, ok := s.revisions[revision]
	if !ok || pinned.Preset.Name != name {
		return presets.Record{}, presets.ErrNotFound
	}
	return pinned, nil
}

// capturingChatProxy records the chat request the resolver forwarded.
type capturingChatProxy struct {
	Proxy
	lastChat *ChatCompletionRequest
}

func (p *capturingChatProxy) ProcessChatCompletion(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	p.lastChat = req
	return &ChatCompletionResponse{}, nil
}

func floatPointer(value float32) *float32 { return &value }
func intPointer(value int) *int           { return &value }

func presetTestRecord() presets.Record {
	falseValue := false
	return presets.Record{
		Revision: 1,
		Preset: presets.Preset{
			Name: "fast",
			Config: presets.Config{
				Model:       "openai/gpt-4o-mini",
				Models:      []string{"groq/llama-3.3-70b"},
				System:      "Answer briefly.",
				Temperature: floatPointer(0.2),
				MaxTokens:   intPointer(256),
				Provider: &presets.ProviderPreferences{
					Order:                   []string{"groq", "openai"},
					AllowFallbacks:          &falseValue,
					Sort:                    presets.SortPrice,
					MaxPromptPricePer1M:     2.5,
					MaxCompletionPricePer1M: 10,
				},
			},
		},
	}
}

func TestChatReferenceMergesPresetConfig(t *testing.T) {
	inner := &capturingChatProxy{}
	wrapped := NewPresetResolver(staticPresetSource{record: presetTestRecord()}).Wrap(inner)

	request := &ChatCompletionRequest{
		Request: inference.ChatRequest{
			Model: "@preset/fast",
			Messages: []inference.Message{{
				Role:    inference.RoleUser,
				Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}},
			}},
		},
	}
	_, err := wrapped.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, inner.lastChat)

	resolved := inner.lastChat.Request
	require.Equal(t, "openai/gpt-4o-mini", resolved.Model)
	require.Equal(t, []string{"groq/llama-3.3-70b"}, resolved.FallbackModels)
	require.Equal(t, float32(0.2), *resolved.Sampling.Temperature)
	require.Equal(t, 256, *resolved.Sampling.MaxTokens)
	require.Len(t, resolved.Messages, 2, "the preset system prompt is prepended")
	require.Equal(t, inference.RoleSystem, resolved.Messages[0].Role)
	require.Equal(t, "Answer briefly.", resolved.Messages[0].Content[0].Text)
	require.NotNil(t, inner.lastChat.Provider)
	require.Equal(t, []string{"groq", "openai"}, inner.lastChat.Provider.Order)
	require.False(t, inner.lastChat.Provider.AllowFallback)
	require.Equal(t, presets.SortPrice, inner.lastChat.Provider.Sort)
	require.InDelta(t, 2.5, inner.lastChat.Provider.MaxPromptPricePer1M, 1e-9)
	require.InDelta(t, 10.0, inner.lastChat.Provider.MaxCompletionPricePer1M, 1e-9)
}

func TestPresetRequestFieldsWin(t *testing.T) {
	inner := &capturingChatProxy{}
	wrapped := NewPresetResolver(staticPresetSource{record: presetTestRecord()}).Wrap(inner)

	request := &ChatCompletionRequest{
		Preset: "fast",
		Request: inference.ChatRequest{
			Model: "anthropic/claude-sonnet-5",
			Messages: []inference.Message{
				{
					Role:    inference.RoleSystem,
					Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "Answer in French."}},
				},
				{
					Role:    inference.RoleUser,
					Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}},
				},
			},
			Sampling: inference.Sampling{Temperature: floatPointer(0.9)},
		},
		Provider: &ProviderPreferences{Only: []string{"anthropic"}, AllowFallback: true},
	}
	_, err := wrapped.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)
	require.NotNil(t, inner.lastChat)

	resolved := inner.lastChat.Request
	// The request-supplied model, temperature, system prompt, and provider
	// policy all win over the preset.
	require.Equal(t, "anthropic/claude-sonnet-5", resolved.Model)
	require.Empty(t, resolved.FallbackModels)
	require.Equal(t, float32(0.9), *resolved.Sampling.Temperature)
	require.Len(t, resolved.Messages, 2)
	require.Equal(t, "Answer in French.", resolved.Messages[0].Content[0].Text)
	require.Equal(t, []string{"anthropic"}, inner.lastChat.Provider.Only)
	require.Empty(t, inner.lastChat.Provider.Order)
	// Absent request fields still inherit preset values.
	require.Equal(t, 256, *resolved.Sampling.MaxTokens)
}

func TestUnknownPresetRejected(t *testing.T) {
	inner := &capturingChatProxy{}
	wrapped := NewPresetResolver(staticPresetSource{record: presetTestRecord()}).Wrap(inner)

	request := &ChatCompletionRequest{
		Request: inference.ChatRequest{Model: "@preset/missing"},
	}
	_, err := wrapped.ProcessChatCompletion(context.Background(), request)
	require.ErrorIs(t, err, ErrPresetNotFound)
	require.Contains(t, err.Error(), "missing")
	require.Nil(t, inner.lastChat, "an unknown preset never reaches routing")

	// The stream path fails the same way.
	_, err = wrapped.ProcessChatCompletionStream(context.Background(), &ChatCompletionRequest{
		Request: inference.ChatRequest{Model: "@preset/missing", Stream: true},
	})
	require.ErrorIs(t, err, ErrPresetNotFound)

	// A storage failure is not a not-found: it surfaces as an internal error.
	storageDown := NewPresetResolver(staticPresetSource{err: errors.New("store offline")}).Wrap(inner)
	_, err = storageDown.ProcessChatCompletion(context.Background(), &ChatCompletionRequest{
		Request: inference.ChatRequest{Model: "@preset/fast"},
	})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrPresetNotFound)

	// A request without any preset selection passes through untouched.
	plain := &ChatCompletionRequest{Request: inference.ChatRequest{Model: "openai/gpt-4o"}}
	_, err = wrapped.ProcessChatCompletion(context.Background(), plain)
	require.NoError(t, err)
	require.Equal(t, "openai/gpt-4o", inner.lastChat.Request.Model)
}

func TestPinnedPresetReferenceUsesTheRevision(t *testing.T) {
	head := presetTestRecord()
	head.Revision = 3
	head.Preset.Config.Model = "openai/gpt-4o"
	pinned := presetTestRecord()
	pinned.Revision = 2
	source := staticPresetSource{record: head, revisions: map[uint64]presets.Record{2: pinned}}

	inner := &capturingChatProxy{}
	wrapped := NewPresetResolver(source).Wrap(inner)

	// The model-field reference resolves the pinned revision verbatim.
	_, err := wrapped.ProcessChatCompletion(context.Background(), &ChatCompletionRequest{
		Request: inference.ChatRequest{Model: "@preset/fast@2"},
	})
	require.NoError(t, err)
	require.Equal(t, "openai/gpt-4o-mini", inner.lastChat.Request.Model)

	// The body field pins the same way.
	_, err = wrapped.ProcessChatCompletion(context.Background(), &ChatCompletionRequest{
		Preset:  "fast@2",
		Request: inference.ChatRequest{Messages: nil},
	})
	require.NoError(t, err)
	require.Equal(t, "openai/gpt-4o-mini", inner.lastChat.Request.Model)

	// Without a pin, the head answers.
	_, err = wrapped.ProcessChatCompletion(context.Background(), &ChatCompletionRequest{
		Request: inference.ChatRequest{Model: "@preset/fast"},
	})
	require.NoError(t, err)
	require.Equal(t, "openai/gpt-4o", inner.lastChat.Request.Model)
}

func TestPinnedPresetReferenceRejectsBadPins(t *testing.T) {
	inner := &capturingChatProxy{}
	wrapped := NewPresetResolver(staticPresetSource{record: presetTestRecord()}).Wrap(inner)

	for _, model := range []string{"@preset/fast@abc", "@preset/fast@0", "@preset/fast@", "@preset/fast@9"} {
		_, err := wrapped.ProcessChatCompletion(context.Background(), &ChatCompletionRequest{
			Request: inference.ChatRequest{Model: model},
		})
		require.ErrorIs(t, err, ErrPresetNotFound, model)
	}
	require.Nil(t, inner.lastChat, "a bad pin never reaches routing")
}
