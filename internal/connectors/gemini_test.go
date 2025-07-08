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

func TestNewGeminiConnector(t *testing.T) {
	tests := []struct {
		name    string
		config  ProviderConfig
		wantErr bool
		isVertex bool
	}{
		{
			name: "valid config for Gemini API",
			config: ProviderConfig{
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
			isVertex: false,
		},
		{
			name: "valid config for Vertex AI",
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
			isVertex: true,
		},
		{
			name: "custom base URL",
			config: ProviderConfig{
				BaseURL:        "https://custom.gemini.api",
				APIKey:         "test-key",
				Timeout:        30 * time.Second,
				MaxConnections: 100,
			},
			wantErr: false,
			isVertex: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector, err := NewGeminiConnector(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewGeminiConnector() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil {
				if connector == nil {
					t.Error("NewGeminiConnector() returned nil connector")
				}
				if connector.config.BaseURL == "" {
					t.Error("NewGeminiConnector() base URL not set")
				}
				if connector.isVertexAI != tt.isVertex {
					t.Errorf("Expected isVertexAI %v, got %v", tt.isVertex, connector.isVertexAI)
				}
			}
		})
	}
}

func TestGeminiConnector_Chat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify API key in query param for Gemini API
		if !strings.Contains(r.URL.Path, "generateContent") {
			t.Errorf("Expected generateContent in path, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("Expected API key in query param, got %s", r.URL.Query().Get("key"))
		}

		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		// Verify request format
		contents, ok := reqBody["contents"].([]interface{})
		if !ok || len(contents) == 0 {
			t.Error("Expected contents in request")
		}

		resp := geminiResponse{
			Candidates: []struct {
				Content struct {
					Parts []map[string]interface{} `json:"parts"`
					Role  string                   `json:"role"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
				Index        int    `json:"index"`
			}{
				{
					Content: struct {
						Parts []map[string]interface{} `json:"parts"`
						Role  string                   `json:"role"`
					}{
						Parts: []map[string]interface{}{
							{"text": "Hello from Gemini!"},
						},
						Role: "model",
					},
					FinishReason: "STOP",
					Index:        0,
				},
			},
			UsageMetadata: struct {
				PromptTokenCount     int `json:"promptTokenCount"`
				CandidatesTokenCount int `json:"candidatesTokenCount"`
				TotalTokenCount      int `json:"totalTokenCount"`
			}{
				PromptTokenCount:     5,
				CandidatesTokenCount: 4,
				TotalTokenCount:      9,
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	connector, _ := NewGeminiConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1beta",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
		MaxRetries:     0,
	})

	resp, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "gemini-pro",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if resp.Choices[0].Message.Content != "Hello from Gemini!" {
		t.Errorf("Expected 'Hello from Gemini!', got '%s'", resp.Choices[0].Message.Content)
	}

	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("Expected finish reason 'stop', got '%s'", resp.Choices[0].FinishReason)
	}
}

func TestGeminiConnector_SystemMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		// Check that system message was prepended to first user message
		contents := reqBody["contents"].([]interface{})
		firstContent := contents[0].(map[string]interface{})
		parts := firstContent["parts"].([]interface{})
		firstPart := parts[0].(map[string]interface{})
		text := firstPart["text"].(string)

		if !strings.HasPrefix(text, "You are a helpful assistant") {
			t.Errorf("Expected system message prepended, got: %s", text)
		}

		resp := geminiResponse{
			Candidates: []struct {
				Content struct {
					Parts []map[string]interface{} `json:"parts"`
					Role  string                   `json:"role"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
				Index        int    `json:"index"`
			}{
				{
					Content: struct {
						Parts []map[string]interface{} `json:"parts"`
						Role  string                   `json:"role"`
					}{
						Parts: []map[string]interface{}{
							{"text": "OK"},
						},
						Role: "model",
					},
					FinishReason: "STOP",
				},
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	connector, _ := NewGeminiConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1beta",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	_, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "gemini-pro",
		Messages: []Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello"},
		},
	})

	if err != nil {
		t.Fatalf("Chat() with system message error = %v", err)
	}
}

func TestGeminiConnector_Embeddings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "embedContent") {
			t.Errorf("Expected embedContent in path, got %s", r.URL.Path)
		}

		resp := struct {
			Embedding struct {
				Values []float32 `json:"values"`
			} `json:"embedding"`
		}{
			Embedding: struct {
				Values []float32 `json:"values"`
			}{
				Values: []float32{0.1, 0.2, 0.3, 0.4, 0.5},
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	connector, _ := NewGeminiConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1beta",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	resp, err := connector.Embeddings(context.Background(), &EmbeddingsRequest{
		Model: "embedding-001",
		Input: "Hello world",
	})
	if err != nil {
		t.Fatalf("Embeddings() error = %v", err)
	}

	if len(resp.Data) != 1 {
		t.Errorf("Expected 1 embedding, got %d", len(resp.Data))
	}
	if len(resp.Data[0].Embedding) != 5 {
		t.Errorf("Expected 5 dimensions, got %d", len(resp.Data[0].Embedding))
	}
}

func TestGeminiConnector_Models(t *testing.T) {
	connector, _ := NewGeminiConnector(ProviderConfig{
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	resp, err := connector.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}

	// Check for expected models
	modelMap := make(map[string]bool)
	for _, model := range resp.Data {
		modelMap[model.ID] = true
	}

	expectedModels := []string{
		"google/gemini-2.5-pro",
		"google/gemini-2.5-flash",
		"google/gemini-2.0-flash-lite",
		"google/gemini-1.5-pro-002",
		"google/gemini-1.5-pro",
		"google/gemini-1.5-flash-002",
		"google/gemini-1.5-flash",
		"google/gemini-1.5-flash-8b-001",
		"google/gemini-pro",
		"google/gemini-pro-vision",
		"google/text-embedding-004",
		"google/embedding-001",
	}

	for _, expected := range expectedModels {
		if !modelMap[expected] {
			t.Errorf("Expected model %s not found", expected)
		}
	}
}

func TestGeminiConnector_VertexAI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Vertex AI uses Bearer token instead of API key
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Expected Bearer token for Vertex AI, got %s", r.Header.Get("Authorization"))
		}

		// Verify path includes project and location
		if !strings.Contains(r.URL.Path, "publishers/google/models") {
			t.Errorf("Expected Vertex AI path format, got %s", r.URL.Path)
		}

		resp := geminiResponse{
			Candidates: []struct {
				Content struct {
					Parts []map[string]interface{} `json:"parts"`
					Role  string                   `json:"role"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
				Index        int    `json:"index"`
			}{
				{
					Content: struct {
						Parts []map[string]interface{} `json:"parts"`
						Role  string                   `json:"role"`
					}{
						Parts: []map[string]interface{}{
							{"text": "Hello from Vertex AI!"},
						},
						Role: "model",
					},
					FinishReason: "STOP",
				},
			},
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	connector, _ := NewGeminiConnector(ProviderConfig{
		BaseURL:        server.URL,
		APIKey:         "test-token",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
		Extra: map[string]interface{}{
			"project_id": "my-project",
			"location":   "us-central1",
		},
	})

	resp, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "gemini-pro",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	if err != nil {
		t.Fatalf("Chat() Vertex AI error = %v", err)
	}

	if resp.Choices[0].Message.Content != "Hello from Vertex AI!" {
		t.Errorf("Expected 'Hello from Vertex AI!', got '%s'", resp.Choices[0].Message.Content)
	}
}

func TestGeminiConnector_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]interface{}{
				"code":    400,
				"message": "Invalid request",
				"status":  "INVALID_ARGUMENT",
			},
		})
	}))
	defer server.Close()

	connector, _ := NewGeminiConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1beta",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
		MaxRetries:     0,
	})

	_, err := connector.Chat(context.Background(), &ChatRequest{
		Model: "gemini-pro",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})

	if err == nil {
		t.Error("Expected error for bad request")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Errorf("Expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", apiErr.StatusCode)
	}
	if apiErr.Provider != "gemini" {
		t.Errorf("Expected provider 'gemini', got '%s'", apiErr.Provider)
	}
}

func TestGeminiConnector_Name(t *testing.T) {
	connector, _ := NewGeminiConnector(ProviderConfig{
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})
	
	if name := connector.Name(); name != "gemini" {
		t.Errorf("Expected name 'gemini', got '%s'", name)
	}
}

func TestGeminiConnector_ChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "streamGenerateContent") {
			t.Errorf("Expected streamGenerateContent in path, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send Gemini-style streaming responses
		chunks := []geminiResponse{
			{
				Candidates: []struct {
					Content struct {
						Parts []map[string]interface{} `json:"parts"`
						Role  string                   `json:"role"`
					} `json:"content"`
					FinishReason string `json:"finishReason"`
					Index        int    `json:"index"`
				}{
					{
						Content: struct {
							Parts []map[string]interface{} `json:"parts"`
							Role  string                   `json:"role"`
						}{
							Parts: []map[string]interface{}{
								{"text": "Hello"},
							},
							Role: "model",
						},
						FinishReason: "",
						Index:        0,
					},
				},
			},
			{
				Candidates: []struct {
					Content struct {
						Parts []map[string]interface{} `json:"parts"`
						Role  string                   `json:"role"`
					} `json:"content"`
					FinishReason string `json:"finishReason"`
					Index        int    `json:"index"`
				}{
					{
						Content: struct {
							Parts []map[string]interface{} `json:"parts"`
							Role  string                   `json:"role"`
						}{
							Parts: []map[string]interface{}{
								{"text": " from Gemini!"},
							},
							Role: "model",
						},
						FinishReason: "STOP",
						Index:        0,
					},
				},
			},
		}

		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			w.Write(data)
			w.Write([]byte("\n"))
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	connector, _ := NewGeminiConnector(ProviderConfig{
		BaseURL:        server.URL + "/v1beta",
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})

	stream, err := connector.ChatStream(context.Background(), &ChatRequest{
		Model: "gemini-pro",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	defer stream.Close()

	// Read chunks
	var content string
	chunkCount := 0
	for chunkCount < 2 { // Read 2 chunks
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv() error = %v", err)
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			content += chunk.Choices[0].Delta.Content
		}
		chunkCount++
	}

	if content != "Hello from Gemini!" {
		t.Errorf("Expected 'Hello from Gemini!', got '%s'", content)
	}
}

func TestGeminiConnector_Health(t *testing.T) {
	tests := []struct {
		name       string
		mockStatus int
		mockError  bool
		wantErr    bool
	}{
		{
			name:       "service healthy",
			mockStatus: http.StatusOK,
			mockError:  false,
			wantErr:    false,
		},
		{
			name:       "service up but unauthorized",
			mockStatus: http.StatusUnauthorized,
			mockError:  true,
			wantErr:    false, // Auth error means service is up
		},
		{
			name:       "service down",
			mockStatus: http.StatusInternalServerError,
			mockError:  true,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.mockError {
					w.WriteHeader(tt.mockStatus)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error": map[string]interface{}{
							"code":    tt.mockStatus,
							"message": "Test error",
							"status":  "ERROR",
						},
					})
					return
				}
				
				w.WriteHeader(tt.mockStatus)
				json.NewEncoder(w).Encode(geminiResponse{
					Candidates: []struct {
						Content struct {
							Parts []map[string]interface{} `json:"parts"`
							Role  string                   `json:"role"`
						} `json:"content"`
						FinishReason string `json:"finishReason"`
						Index        int    `json:"index"`
					}{
						{
							Content: struct {
								Parts []map[string]interface{} `json:"parts"`
								Role  string                   `json:"role"`
							}{
								Parts: []map[string]interface{}{
									{"text": "H"},
								},
								Role: "model",
							},
							FinishReason: "STOP",
						},
					},
				})
			}))
			defer server.Close()

			connector, _ := NewGeminiConnector(ProviderConfig{
				BaseURL:        server.URL + "/v1beta",
				APIKey:         "test-key",
				Timeout:        5 * time.Second,
				MaxConnections: 10,
				MaxRetries:     0,
			})

			err := connector.Health(context.Background())
			if (err != nil) != tt.wantErr {
				t.Errorf("Health() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGeminiConnector_Close(t *testing.T) {
	connector, _ := NewGeminiConnector(ProviderConfig{
		APIKey:         "test-key",
		Timeout:        5 * time.Second,
		MaxConnections: 10,
	})
	
	if err := connector.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

