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
)

func TestNewGoogleAIStudioConnector(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: ProviderConfig{
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
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
				BaseURL:        "",
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
			if err == nil && connector.Name() != "google-aistudio" {
				t.Errorf("Expected name 'google-aistudio', got %s", connector.Name())
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
				APIKey:         "test-token",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
				Extra: map[string]interface{}{
					"project_id": "my-project",
					"location":   "us-central1",
				},
			},
			wantErr: false,
		},
		{
			name: "missing project_id",
			config: ProviderConfig{
				APIKey:         "test-token",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: true,
		},
		{
			name: "default location",
			config: ProviderConfig{
				APIKey:         "test-token",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
				Extra: map[string]interface{}{
					"project_id": "my-project",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector, err := NewVertexAIConnector(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewVertexAIConnector() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && connector.Name() != "google-vertexai" {
				t.Errorf("Expected name 'google-vertexai', got %s", connector.Name())
			}
		})
	}
}

func TestGoogleAIStudioConnector_Chat(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if !strings.Contains(r.URL.Path, ":generateContent") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("key") == "" {
			t.Error("missing API key in query")
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
		Model: "google-aistudio/gemini-1.5-flash",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: intPtr(100),
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
		BaseURL: server.URL,
		APIKey:  "test-token",
		Timeout: 10 * time.Second,
		Extra: map[string]interface{}{
			"project_id": "test-project",
			"location":   "us-central1",
		},
	})
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}

	resp, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "google-vertexai/gemini-1.5-flash",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: intPtr(100),
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

func TestGoogleAIStudioConnector_Models(t *testing.T) {
	connector, err := NewGoogleAIStudioConnector(ProviderConfig{
		APIKey:  "test-key",
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}

	// Clear cache to force static list
	clearModelCache("google-aistudio")

	models, err := connector.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() failed: %v", err)
	}

	if len(models.Data) == 0 {
		t.Error("expected models, got none")
	}

	// Check that all models have correct prefix
	for _, model := range models.Data {
		if !strings.HasPrefix(model.ID, "google-aistudio/") {
			t.Errorf("model ID missing prefix: %s", model.ID)
		}
	}
}

func TestVertexAIConnector_Models(t *testing.T) {
	connector, err := NewVertexAIConnector(ProviderConfig{
		APIKey:  "test-token",
		Timeout: 10 * time.Second,
		Extra: map[string]interface{}{
			"project_id": "test-project",
		},
	})
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}

	models, err := connector.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() failed: %v", err)
	}

	if len(models.Data) == 0 {
		t.Error("expected models, got none")
	}

	// Check that we have different types of models
	foundGemini := false
	foundClaude := false
	foundPaLM := false
	foundCodey := false

	for _, model := range models.Data {
		if !strings.HasPrefix(model.ID, "google-vertexai/") {
			t.Errorf("model ID missing prefix: %s", model.ID)
		}

		if strings.Contains(model.ID, "gemini") {
			foundGemini = true
		}
		if strings.Contains(model.ID, "claude") {
			foundClaude = true
		}
		if strings.Contains(model.ID, "bison") {
			foundPaLM = true
		}
		if strings.Contains(model.ID, "code") {
			foundCodey = true
		}
	}

	if !foundGemini {
		t.Error("expected Gemini models, found none")
	}
	if !foundClaude {
		t.Error("expected Claude models via Vertex AI, found none")
	}
	if !foundPaLM {
		t.Error("expected PaLM models, found none")
	}
	if !foundCodey {
		t.Error("expected Codey models, found none")
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
		Model: "google-aistudio/gemini-1.5-flash",
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
