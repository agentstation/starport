// Command sdk-smoke-server serves deterministic OpenRouter protocol fixtures.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/protocol/openrouter"
)

func main() {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server := &http.Server{Handler: routes(), ReadHeaderTimeout: 5 * time.Second}
	fmt.Printf("http://%s\n", listener.Addr().String())
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			panic(serveErr)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	_ = server.Close()
}

func routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/chat/completions", authorize(chat))
	mux.HandleFunc("POST /api/v1/embeddings", authorize(embeddings))
	mux.HandleFunc("GET /api/v1/models", authorize(models))
	return mux
}

func authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			openrouter.WriteError(w, http.StatusUnauthorized, "Missing API key", map[string]any{"error_type": "authentication_error"})
			return
		}
		next(w, r)
	}
}

func chat(w http.ResponseWriter, r *http.Request) {
	request, err := openrouter.DecodeChat(r.Body)
	if err != nil {
		openrouter.WriteError(w, http.StatusBadRequest, err.Error(), map[string]any{"error_type": "invalid_request_error"})
		return
	}
	model := request.Inference.Model
	if model == "" && len(request.Inference.FallbackModels) > 0 {
		model = request.Inference.FallbackModels[0]
	}
	response := inference.ChatResponse{
		ID: "chatcmpl-smoke", CreatedUnix: 1744329600, Model: model, ModelUsed: model,
		Choices: []inference.Choice{{
			Index: 0, FinishReason: "stop",
			Message: inference.Message{Role: inference.RoleAssistant, Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "starport smoke ok"}}},
		}},
		Usage: inference.Usage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7},
	}
	if !request.Inference.Stream {
		_ = openrouter.WriteJSON(w, http.StatusOK, openrouter.EncodeChat(response))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	events := []inference.StreamEvent{
		{Kind: inference.StreamStart, ID: response.ID, CreatedUnix: response.CreatedUnix, Model: model, ModelUsed: model, Deltas: []inference.ChoiceDelta{{Index: 0, Role: inference.RoleAssistant}}},
		{Kind: inference.StreamDelta, ID: response.ID, CreatedUnix: response.CreatedUnix, Model: model, ModelUsed: model, Deltas: []inference.ChoiceDelta{{Index: 0, Text: "starport smoke ok"}}},
		{Kind: inference.StreamEnd, ID: response.ID, CreatedUnix: response.CreatedUnix, Model: model, ModelUsed: model, Deltas: []inference.ChoiceDelta{{Index: 0, FinishReason: "stop"}}},
	}
	if request.Inference.StreamOptions.IncludeUsage {
		usage := response.Usage
		events = append(events, inference.StreamEvent{Kind: inference.StreamUsage, ID: response.ID, Model: model, ModelUsed: model, Usage: &usage})
	}
	for _, event := range events {
		data, marshalErr := json.Marshal(openrouter.EncodeStream(event))
		if marshalErr != nil {
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func embeddings(w http.ResponseWriter, r *http.Request) {
	request, err := openrouter.DecodeEmbedding(r.Body)
	if err != nil {
		openrouter.WriteError(w, http.StatusBadRequest, err.Error(), map[string]any{"error_type": "invalid_request_error"})
		return
	}
	_ = openrouter.WriteJSON(w, http.StatusOK, openrouter.EncodeEmbedding(inference.EmbeddingResponse{
		Model: request.Model, Data: []inference.Embedding{{Index: 0, Vector: []float32{0.1, 0.2, 0.3}}},
		Usage: inference.Usage{InputTokens: 1, TotalTokens: 1},
	}))
}

func models(w http.ResponseWriter, _ *http.Request) {
	_ = openrouter.WriteJSON(w, http.StatusOK, openrouter.ModelList{
		Data: []openrouter.Model{{
			ID: "openai/gpt-4.1", CanonicalSlug: "openai/gpt-4.1", Name: "GPT-4.1",
			Created: 1744329600, ContextLength: 128000,
			Pricing:             &openrouter.Pricing{Prompt: "0.000002", Completion: "0.000008"},
			SupportedParameters: []string{"tools", "response_format"},
		}},
		TotalCount: 1, Links: openrouter.ModelLinks{Next: nil},
	})
}
