package connectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/providerauth"
)

func TestNewGoogleAIStudioConnector(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
	}{
		{
			name: "missing catalog base URL",
			config: ProviderConfig{
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: true,
		},
		{
			name: "custom base URL",
			config: ProviderConfig{
				BaseURL:        "https://custom.api",
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
		},
		{
			name: "invalid config - missing timeout",
			config: ProviderConfig{
				BaseURL:        "https://provider.test",
				APIKey:         "test-key",
				Timeout:        0,
				MaxConnections: 100,
			},
			wantErr: false, // Validate sets default timeout
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector, err := NewGoogleAIStudioConnector(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewGoogleAIStudioConnector() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && connector.Name() != "google-ai-studio" {
				t.Errorf("Expected name 'google-ai-studio', got %s", connector.Name())
			}
		})
	}
}

func TestNewVertexAIConnector(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: ProviderConfig{
				BaseURL:        "https://provider.test",
				APIKey:         "test-token",
				AuthMode:       providerauth.ModeStatic,
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
		},
		{
			name: "project identity is validated by adapter registry",
			config: ProviderConfig{
				BaseURL:        "https://provider.test",
				APIKey:         "test-token",
				AuthMode:       providerauth.ModeStatic,
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
		},
		{
			name: "default location",
			config: ProviderConfig{
				BaseURL:        "https://provider.test",
				APIKey:         "test-token",
				AuthMode:       providerauth.ModeStatic,
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
		},
		{
			name: "missing auth mode",
			config: ProviderConfig{
				BaseURL: "https://provider.test",
				APIKey:  "test-token",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector, err := NewVertexAIConnector(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewVertexAIConnector() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && connector.Name() != "google-vertex" {
				t.Errorf("Expected name 'google-vertex', got %s", connector.Name())
			}
		})
	}
}

func TestGoogleAPIKeyUsesInferenceHeader(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if !strings.Contains(r.URL.Path, ":generateContent") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") == "" {
			t.Error("missing API key header")
		}
		if r.URL.Query().Get("key") != "" {
			t.Error("API key entered the request URL")
		}

		// Send response
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Parts: []geminiPart{
							{Text: "Hello from Google AI Studio!", Thought: false},
						},
						Role: "model",
					},
					FinishReason: "STOP",
					Index:        0,
				},
			},
			UsageMetadata: geminiUsageMetadata{
				PromptTokenCount:     10,
				CandidatesTokenCount: 20,
				TotalTokenCount:      30,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	connector, err := NewGoogleAIStudioConnector(ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}

	resp, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "google-ai-studio/gemini-1.5-flash",
		Endpoint: InferenceEndpoint{
			Type: catalogs.EndpointTypeGoogle,
			URL:  server.URL + "/v1beta/models/gemini-1.5-flash:generateContent",
		},
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: IntPtr(100),
	})

	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Errorf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello from Google AI Studio!" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 30 {
		t.Errorf("expected 30 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestVertexAIConnector_Chat(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if !strings.Contains(r.URL.Path, ":generateContent") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing or invalid Authorization header")
		}

		// Send response
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Parts: []geminiPart{
							{Text: "Hello from Vertex AI!", Thought: false},
						},
						Role: "model",
					},
					FinishReason: "STOP",
					Index:        0,
				},
			},
			UsageMetadata: geminiUsageMetadata{
				PromptTokenCount:     10,
				CandidatesTokenCount: 20,
				TotalTokenCount:      30,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	connector, err := NewVertexAIConnector(ProviderConfig{
		BaseURL:  server.URL,
		APIKey:   "test-token",
		AuthMode: providerauth.ModeStatic,
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}

	resp, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "google-vertex/gemini-1.5-flash",
		Endpoint: InferenceEndpoint{
			Type: catalogs.EndpointTypeGoogleCloud,
			URL:  server.URL + "/v1/projects/test/locations/us/publishers/google/models/gemini:generateContent",
		},
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: IntPtr(100),
	})

	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Errorf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello from Vertex AI!" {
		t.Errorf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 30 {
		t.Errorf("expected 30 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestGoogleAIStudioConnector_ChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":streamGenerateContent") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		// Stream response chunks
		chunks := []string{"Hello", " from", " stream!"}
		for i, chunk := range chunks {
			resp := geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Parts: []geminiPart{
								{Text: chunk, Thought: false},
							},
							Role: "model",
						},
						FinishReason: "",
						Index:        0,
					},
				},
			}

			if i == len(chunks)-1 {
				resp.Candidates[0].FinishReason = "STOP"
			}

			data, _ := json.Marshal(resp)
			w.Write(data)
			w.Write([]byte("\n"))
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	connector, err := NewGoogleAIStudioConnector(ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}

	stream, err := connector.ChatStream(context.Background(), &ChatRequest{
		Model: "google-ai-studio/gemini-1.5-flash",
		Endpoint: InferenceEndpoint{
			Type: catalogs.EndpointTypeGoogle,
			URL:  server.URL + "/v1beta/models/gemini-1.5-flash:streamGenerateContent",
		},
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	defer stream.Close()

	var content strings.Builder
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream recv failed: %v", err)
		}
		if len(chunk.Choices) > 0 {
			content.WriteString(chunk.Choices[0].Delta.Content)
		}
	}

	if content.String() != "Hello from stream!" {
		t.Errorf("expected 'Hello from stream!', got '%s'", content.String())
	}
}

func TestGoogleAIStudioConnector_EmbeddingsUsesOfferingEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/selected/embed", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"embedding": map[string]any{"values": []float32{0.1, 0.2}},
		}))
	}))
	defer server.Close()

	connector, err := NewGoogleAIStudioConnector(ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "inference-key",
	})
	require.NoError(t, err)
	defer connector.Close()
	response, err := connector.Embeddings(context.Background(), &EmbeddingsRequest{
		Model: "opaque/embedding@001",
		Input: []string{"first", "second"},
		Endpoint: InferenceEndpoint{
			Type: catalogs.EndpointTypeGoogle,
			URL:  server.URL + "/selected/embed",
		},
	})
	require.NoError(t, err)
	require.Len(t, response.Data, 2)
	require.Equal(t, "opaque/embedding@001", response.Model)
}
