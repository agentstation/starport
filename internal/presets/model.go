// Package presets owns reusable inference configuration presets and persistence.
package presets

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var validNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ErrInvalidPreset reports a preset that fails validation.
var ErrInvalidPreset = errors.New("invalid preset")

// Preset is one reusable inference configuration. The name is the reference:
// requests select a preset with "@preset/<name>" or a "preset" body field.
type Preset struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Config      Config    `json:"config"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Config is the typed preset subset a request may inherit: model selection,
// provider preferences, a system prompt, and sampling overrides. Fields the
// request supplies win over preset fields.
type Config struct {
	Model            string               `json:"model,omitempty"`
	Models           []string             `json:"models,omitempty"`
	Provider         *ProviderPreferences `json:"provider,omitempty"`
	System           string               `json:"system,omitempty"`
	Temperature      *float32             `json:"temperature,omitempty"`
	TopP             *float32             `json:"top_p,omitempty"`
	PresencePenalty  *float32             `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float32             `json:"frequency_penalty,omitempty"`
	MaxTokens        *int                 `json:"max_tokens,omitempty"`
	Seed             *int                 `json:"seed,omitempty"`
	Stop             []string             `json:"stop,omitempty"`
}

// ProviderPreferences is the preset-owned provider routing policy.
type ProviderPreferences struct {
	Order          []string `json:"order,omitempty"`
	Only           []string `json:"only,omitempty"`
	Ignore         []string `json:"ignore,omitempty"`
	AllowFallbacks *bool    `json:"allow_fallbacks,omitempty"`
}

// IsZero reports whether the config carries no settings at all.
func (c Config) IsZero() bool {
	return c.Model == "" && len(c.Models) == 0 && c.Provider == nil && c.System == "" &&
		c.Temperature == nil && c.TopP == nil && c.PresencePenalty == nil &&
		c.FrequencyPenalty == nil && c.MaxTokens == nil && c.Seed == nil && len(c.Stop) == 0
}

// Clone returns a deep copy of the config.
func (c Config) Clone() Config {
	clone := c
	clone.Models = append([]string(nil), c.Models...)
	clone.Stop = append([]string(nil), c.Stop...)
	clone.Temperature = clonePointer(c.Temperature)
	clone.TopP = clonePointer(c.TopP)
	clone.PresencePenalty = clonePointer(c.PresencePenalty)
	clone.FrequencyPenalty = clonePointer(c.FrequencyPenalty)
	clone.MaxTokens = clonePointer(c.MaxTokens)
	clone.Seed = clonePointer(c.Seed)
	if c.Provider != nil {
		provider := ProviderPreferences{
			Order:          append([]string(nil), c.Provider.Order...),
			Only:           append([]string(nil), c.Provider.Only...),
			Ignore:         append([]string(nil), c.Provider.Ignore...),
			AllowFallbacks: clonePointer(c.Provider.AllowFallbacks),
		}
		clone.Provider = &provider
	}
	return clone
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

// Validate checks preset invariants.
func (p Preset) Validate() error {
	if p.Name == "" || len(p.Name) > 255 || !validNameRegex.MatchString(p.Name) {
		return fmt.Errorf("%w: invalid name", ErrInvalidPreset)
	}
	if p.Config.IsZero() {
		return fmt.Errorf("%w: empty config", ErrInvalidPreset)
	}
	if p.UpdatedAt.Before(p.CreatedAt) {
		return fmt.Errorf("%w: updated_at must be after or equal to created_at", ErrInvalidPreset)
	}
	return nil
}
