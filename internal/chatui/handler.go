// Package chatui provides a web-based chat interface for interacting with LLM models through Starport.
package chatui

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/agentstation/uuidkey"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/agentstation/starport/internal/apikeys"
	"github.com/agentstation/starport/internal/storage"
)

//go:embed templates/index.html
var indexHTML string

//go:embed static/chat.js
var chatJS string

//go:embed static/chat.css
var chatCSS string

// Handler provides HTTP handlers for the ChatUI interface.
type Handler struct {
	logger   *zerolog.Logger
	template *template.Template
	config   Config
	store    storage.KVStore
}

// Config holds ChatUI-specific configuration.
type Config struct {
	Title       string
	Theme       string
	AllowKeyGen bool
	APIBaseURL  string
	Store       storage.KVStore
}

// NewHandler creates a new ChatUI handler.
func NewHandler(logger *zerolog.Logger, config Config) (*Handler, error) {
	tmpl, err := template.New("index").Parse(indexHTML)
	if err != nil {
		return nil, err
	}

	return &Handler{
		logger:   logger,
		template: tmpl,
		config:   config,
		store:    config.Store,
	}, nil
}

// Index serves the main ChatUI page.
func (h *Handler) Index(w http.ResponseWriter, _ *http.Request) {
	// Override CSP for ChatUI to allow inline scripts and CDN resources for markdown/syntax highlighting
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://cdnjs.cloudflare.com; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://cdnjs.cloudflare.com; img-src 'self' data:; font-src 'self' https://cdn.jsdelivr.net https://cdnjs.cloudflare.com; connect-src 'self'; frame-ancestors 'none';")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	data := struct {
		Title       string
		Theme       string
		APIBaseURL  string
		AllowKeyGen bool
	}{
		Title:       h.config.Title,
		Theme:       h.config.Theme,
		APIBaseURL:  h.config.APIBaseURL,
		AllowKeyGen: h.config.AllowKeyGen,
	}

	if err := h.template.Execute(w, data); err != nil {
		h.logger.Error().Err(err).Msg("failed to render template")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// Static serves static assets.
func (h *Handler) Static(w http.ResponseWriter, r *http.Request) {
	path := chi.URLParam(r, "*")

	var content string
	var contentType string

	switch path {
	case "chat.js":
		content = chatJS
		contentType = "application/javascript"
	case "chat.css":
		content = chatCSS
		contentType = "text/css"
	default:
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")

	if _, err := w.Write([]byte(content)); err != nil {
		h.logger.Error().Err(err).Msg("failed to write static content")
	}
}

// GenerateKey creates a new API key for the chat interface.
func (h *Handler) GenerateKey(w http.ResponseWriter, r *http.Request) {
	if !h.config.AllowKeyGen {
		http.Error(w, "Key generation is disabled", http.StatusForbidden)
		return
	}

	if h.store == nil {
		h.logger.Error().Msg("storage not configured")
		http.Error(w, "Storage not configured", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	// Generate UUID for the key
	keyUUID := uuid.New().String()

	// Create API key using uuidkey with STARPORT prefix
	apiKeyObj, err := uuidkey.NewAPIKey("STARPORT", keyUUID)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to create API key with uuidkey")
		http.Error(w, "Failed to generate API key", http.StatusInternalServerError)
		return
	}

	// The actual key value (only shown once)
	keyValue := apiKeyObj.String()

	// Use the prefix_key format as the ID (without entropy for storage key)
	keyID := fmt.Sprintf("%s_%s", apiKeyObj.Prefix, apiKeyObj.Key)

	// Hash the full key value for storage
	hash := sha256.Sum256([]byte(keyValue))
	hashStr := hex.EncodeToString(hash[:])

	// Create API key model
	apiKey := &apikeys.APIKey{
		ID:        keyID,
		Name:      fmt.Sprintf("ChatUI-Key-%s", time.Now().Format("20060102-1504")),
		Hash:      hashStr,
		Scopes:    []string{"chat:write", "models:read"},
		Active:    true,
		CreatedAt: time.Now(),
		Metadata: map[string]any{
			"source": "chatui",
			"ip":     r.RemoteAddr,
		},
	}

	// Validate the key
	if err := apiKey.Validate(); err != nil {
		h.logger.Error().Err(err).Msg("invalid API key")
		http.Error(w, "Failed to create API key", http.StatusInternalServerError)
		return
	}

	// Store the key
	keyData, err := storage.Serialize(apiKey)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to serialize API key")
		http.Error(w, "Failed to create API key", http.StatusInternalServerError)
		return
	}

	if err := h.store.Set(ctx, storage.APIKeyKey(apiKey.ID), keyData); err != nil {
		h.logger.Error().Err(err).Msg("failed to store API key")
		http.Error(w, "Failed to create API key", http.StatusInternalServerError)
		return
	}

	// Also store hash -> ID mapping for quick lookups
	if err := h.store.Set(ctx, storage.APIKeyHashKey(hashStr), []byte(apiKey.ID)); err != nil {
		h.logger.Error().Err(err).Msg("failed to store API key hash mapping")
		// Try to clean up the key we just stored
		_ = h.store.Delete(ctx, storage.APIKeyKey(apiKey.ID))
		http.Error(w, "Failed to create API key", http.StatusInternalServerError)
		return
	}

	h.logger.Info().
		Str("key_id", apiKey.ID).
		Str("name", apiKey.Name).
		Msg("API key generated via ChatUI")

	response := map[string]any{
		"key":     keyValue,
		"key_id":  apiKey.ID,
		"message": "API key generated successfully. Save this key as it won't be shown again.",
		"scopes":  apiKey.Scopes,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// Routes returns a chi router with all ChatUI routes.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	r.Get("/", h.Index)
	r.Get("/static/*", h.Static)

	if h.config.AllowKeyGen {
		r.Post("/generate-key", h.GenerateKey)
	}

	return r
}
