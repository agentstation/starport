package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/inference"
)

// A completion with no choices carries nothing a later request can reuse.
// Caching it would replay a provider failure as a fresh success.
func TestPutChatRefusesResponseWithoutChoices(t *testing.T) {
	repository, store := openAdmissionRepository(t)

	err := repository.PutChat(
		context.Background(),
		"responsecache:v1:chat:empty",
		inference.ChatResponse{ID: "chat-1", Model: "openai/gpt-4.1"},
	)
	if !errors.Is(err, ErrEmptyResponse) {
		t.Fatalf("PutChat error = %v, want ErrEmptyResponse", err)
	}
	if len(store.values) != 0 {
		t.Fatalf("store holds %d records, want none", len(store.values))
	}
}

// A completion whose only choice carries no content, no tool calls, and no
// reported output tokens is the empty-stream defect shape. Refuse it.
func TestPutChatRefusesContentFreeCompletion(t *testing.T) {
	repository, store := openAdmissionRepository(t)

	err := repository.PutChat(
		context.Background(),
		"responsecache:v1:chat:blank",
		inference.ChatResponse{
			ID:    "chat-1",
			Model: "openai/gpt-4.1",
			Choices: []inference.Choice{{
				Index:        0,
				Message:      inference.Message{Role: inference.RoleAssistant},
				FinishReason: "stop",
			}},
		},
	)
	if !errors.Is(err, ErrEmptyResponse) {
		t.Fatalf("PutChat error = %v, want ErrEmptyResponse", err)
	}
	if len(store.values) != 0 {
		t.Fatalf("store holds %d records, want none", len(store.values))
	}
}

// A tool-call-only completion has no text but is a real, reusable result.
func TestPutChatAdmitsToolCallCompletion(t *testing.T) {
	repository, _ := openAdmissionRepository(t)

	err := repository.PutChat(
		context.Background(),
		"responsecache:v1:chat:tool",
		inference.ChatResponse{
			ID:    "chat-1",
			Model: "openai/gpt-4.1",
			Choices: []inference.Choice{{
				Index: 0,
				Message: inference.Message{
					Role:      inference.RoleAssistant,
					ToolCalls: []inference.ToolCall{{ID: "call-1", Name: "lookup", Arguments: "{}"}},
				},
				FinishReason: "tool_calls",
			}},
			Usage: inference.Usage{InputTokens: 4, OutputTokens: 3, TotalTokens: 7},
		},
	)
	if err != nil {
		t.Fatalf("PutChat error = %v, want admission", err)
	}
}

func openAdmissionRepository(t *testing.T) (Repository, *memoryStore) {
	t.Helper()
	store := newMemoryStore()
	repository, err := Open(store, fixedClock{now: time.Unix(100, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return repository, store
}
