package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/providerauth"
)

func TestVertexAIConnectorRefreshesDefaultCredential(t *testing.T) {
	now := time.Unix(1_000, 0)
	source, err := newTestRefreshingSource(&now)
	if err != nil {
		t.Fatalf("new credential source: %v", err)
	}
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(geminiResponse{
			Candidates: []geminiCandidate{{
				Content:      geminiContent{Parts: []geminiPart{{Text: "ok"}}, Role: "model"},
				FinishReason: "STOP",
			}},
		})
	}))
	defer server.Close()

	connector, err := NewVertexAIConnector(ProviderConfig{
		BaseURL: server.URL, AuthMode: providerauth.ModeDefault,
		CredentialSource: source, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new connector: %v", err)
	}
	defer connector.Close()
	for range 2 {
		_, err = connector.Chat(context.Background(), &ChatRequest{
			Model: "gemini-test", Messages: []Message{{Role: RoleUser, Content: "hello"}},
			Endpoint: InferenceEndpoint{
				Type: catalogs.EndpointTypeGoogleCloud,
				URL:  server.URL + "/v1/projects/test/locations/test/publishers/google/models/test:generateContent",
			},
		})
		if err != nil {
			t.Fatalf("chat: %v", err)
		}
		now = now.Add(9 * time.Minute)
	}
	if want := []string{"Bearer token-1", "Bearer token-2"}; !slices.Equal(authorizations, want) {
		t.Errorf("Authorization values = %v, want %v", authorizations, want)
	}
}

func TestAzureOpenAIConnectorRefreshesDefaultCredential(t *testing.T) {
	now := time.Unix(1_000, 0)
	source, err := newTestRefreshingSource(&now)
	if err != nil {
		t.Fatalf("new credential source: %v", err)
	}
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		if apiKey := request.Header.Get("api-key"); apiKey != "" {
			t.Errorf("api-key = %q with default credentials", apiKey)
		}
		_ = json.NewEncoder(w).Encode(ChatResponse{
			ID: "response", Object: objectChatCompletion, Model: "deployment",
			Choices: []Choice{{Message: Message{Role: RoleAssistant, Content: "ok"}}},
		})
	}))
	defer server.Close()

	connector, err := NewAzureOpenAIConnector(ProviderConfig{
		BaseURL: server.URL, AuthMode: providerauth.ModeDefault,
		CredentialSource: source, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new connector: %v", err)
	}
	defer connector.Close()
	for range 2 {
		_, err = connector.Chat(context.Background(), &ChatRequest{
			Model: "deployment", Messages: []Message{{Role: RoleUser, Content: "hello"}},
			Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL + "/openai/v1/chat/completions"},
		})
		if err != nil {
			t.Fatalf("chat: %v", err)
		}
		now = now.Add(9 * time.Minute)
	}
	if want := []string{"Bearer token-1", "Bearer token-2"}; !slices.Equal(authorizations, want) {
		t.Errorf("Authorization values = %v, want %v", authorizations, want)
	}
}

func TestCloudConnectorsForwardCredentialCancellation(t *testing.T) {
	source := providerauth.SourceFunc(func(ctx context.Context) (providerauth.Token, error) {
		<-ctx.Done()
		return providerauth.Token{}, ctx.Err()
	})
	tests := []struct {
		name string
		chat func(context.Context) error
	}{
		{
			name: "Vertex AI",
			chat: func(ctx context.Context) error {
				connector, err := NewVertexAIConnector(ProviderConfig{
					BaseURL: "https://provider.test", AuthMode: providerauth.ModeDefault,
					CredentialSource: source,
				})
				if err != nil {
					return err
				}
				defer connector.Close()
				_, err = connector.Chat(ctx, &ChatRequest{
					Model: "model", Messages: []Message{{Role: RoleUser, Content: "hello"}},
					Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeGoogleCloud, URL: "https://provider.test/inference"},
				})
				return err
			},
		},
		{
			name: "Azure OpenAI",
			chat: func(ctx context.Context) error {
				connector, err := NewAzureOpenAIConnector(ProviderConfig{
					BaseURL: "https://provider.test", AuthMode: providerauth.ModeDefault,
					CredentialSource: source,
				})
				if err != nil {
					return err
				}
				defer connector.Close()
				_, err = connector.Chat(ctx, &ChatRequest{
					Model: "model", Messages: []Message{{Role: RoleUser, Content: "hello"}},
					Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: "https://provider.test/inference"},
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := test.chat(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("chat error = %v, want context cancellation", err)
			}
		})
	}
}

func newTestRefreshingSource(now *time.Time) (providerauth.Source, error) {
	call := 0
	return providerauth.NewRefreshingSource(
		providerauth.SourceFunc(func(context.Context) (providerauth.Token, error) {
			call++
			return providerauth.Token{
				Value:     fmt.Sprintf("token-%d", call),
				ExpiresAt: now.Add(10 * time.Minute),
			}, nil
		}),
		providerauth.RefreshOptions{
			RefreshBefore: 2 * time.Minute,
			Now:           func() time.Time { return *now },
		},
	)
}
