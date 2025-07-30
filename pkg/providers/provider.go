package providers

import (
	"fmt"
	"sync"
	"time"

	"github.com/agentstation/starport/pkg/models"
)

// Provider represents a configured LLM provider with its models and connector
type Provider struct {
	// Identity
	ID          string // Unique identifier (e.g., "openai-prod", "anthropic-dev")
	Name        string // Display name (e.g., "OpenAI Production")
	Type        string // Provider type (e.g., "openai", "anthropic", "google-vertex")
	Description string // Human-readable description

	// Status
	Enabled bool         // Whether this provider is active
	Health  HealthStatus // Current health status

	// Configuration
	BaseURL string            // API endpoint URL
	APIKey  string            // API key (should be encrypted in production)
	Config  map[string]string // Extra provider-specific configuration

	// Rate limiting
	RateLimitRPM int // Requests per minute limit (0 = unlimited)
	RateLimitTPM int // Tokens per minute limit (0 = unlimited)

	// Models
	mu     sync.RWMutex
	models map[string]*models.Model

	// Statistics
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastUsedAt   time.Time
	RequestCount int64

	// Embedded connector provides the actual API implementation
	// This allows clean usage: provider.Chat() instead of provider.Connector.Chat()
	Connector
}

// HealthStatus represents the health status of a provider
type HealthStatus struct {
	Healthy   bool
	LastCheck time.Time
	LastError error
	Latency   time.Duration
}

// AddModel adds a model to this provider
func (p *Provider) AddModel(model *models.Model) error {
	if model == nil {
		return fmt.Errorf("model cannot be nil")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Set provider if not already set
	if model.Provider == "" {
		model.Provider = p.ID
	}

	// Initialize map if needed
	if p.models == nil {
		p.models = make(map[string]*models.Model)
	}

	// Clone the model to prevent external modifications
	p.models[model.ID] = model.Clone()
	p.UpdatedAt = time.Now()

	return nil
}

// GetModel returns a model by ID
func (p *Provider) GetModel(id string) (*models.Model, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	model, ok := p.models[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrModelNotFound, id)
	}

	// Return a clone to prevent external modifications
	return model.Clone(), nil
}

// ListModels returns all models for this provider
func (p *Provider) ListModels() []*models.Model {
	p.mu.RLock()
	defer p.mu.RUnlock()

	models := make([]*models.Model, 0, len(p.models))
	for _, m := range p.models {
		models = append(models, m.Clone())
	}

	return models
}

// ListActiveModels returns all non-deprecated models
func (p *Provider) ListActiveModels() []*models.Model {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var models []*models.Model
	for _, m := range p.models {
		if m.IsActive() {
			models = append(models, m.Clone())
		}
	}

	return models
}

// HasModel checks if a model exists
func (p *Provider) HasModel(id string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	_, ok := p.models[id]
	return ok
}

// RemoveModel removes a model from this provider
func (p *Provider) RemoveModel(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.models[id]; !ok {
		return fmt.Errorf("%w: %s", ErrModelNotFound, id)
	}

	delete(p.models, id)
	p.UpdatedAt = time.Now()

	return nil
}

// UpdateModel updates an existing model
func (p *Provider) UpdateModel(model *models.Model) error {
	if model == nil {
		return fmt.Errorf("model cannot be nil")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.models[model.ID]; !ok {
		return fmt.Errorf("%w: %s", ErrModelNotFound, model.ID)
	}

	// Update with clone
	p.models[model.ID] = model.Clone()
	p.UpdatedAt = time.Now()

	return nil
}

// ModelCount returns the number of models
func (p *Provider) ModelCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return len(p.models)
}

// IsReady checks if the provider is ready to handle requests
func (p *Provider) IsReady() bool {
	if !p.Enabled {
		return false
	}

	if p.Connector == nil {
		return false
	}

	if p.BaseURL == "" || p.APIKey == "" {
		return false
	}

	return true
}

// UpdateHealth updates the health status
func (p *Provider) UpdateHealth(healthy bool, latency time.Duration, err error) {
	p.Health = HealthStatus{
		Healthy:   healthy,
		LastCheck: time.Now(),
		LastError: err,
		Latency:   latency,
	}
}

// IncrementRequestCount increments the request counter
func (p *Provider) IncrementRequestCount() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.RequestCount++
	p.LastUsedAt = time.Now()
}

// Clone creates a shallow copy of the provider (models are shared)
func (p *Provider) Clone() *Provider {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Create new provider instead of copying struct with mutex
	clone := &Provider{
		ID:           p.ID,
		Name:         p.Name,
		Type:         p.Type,
		Description:  p.Description,
		Enabled:      p.Enabled,
		Health:       p.Health,
		BaseURL:      p.BaseURL,
		APIKey:       p.APIKey,
		Config:       nil,
		RateLimitRPM: p.RateLimitRPM,
		RateLimitTPM: p.RateLimitTPM,
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
		LastUsedAt:   p.LastUsedAt,
		RequestCount: p.RequestCount,
		Connector:    p.Connector,
		models:       p.models,
	}

	// Deep copy config map
	if p.Config != nil {
		clone.Config = make(map[string]string, len(p.Config))
		for k, v := range p.Config {
			clone.Config[k] = v
		}
	}

	return clone
}
