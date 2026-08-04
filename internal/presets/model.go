// Package presets owns reusable inference configuration presets and persistence.
package presets

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var validNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Preset is one reusable inference configuration.
type Preset struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Config      map[string]any `json:"config"`
	Version     int            `json:"version"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// Validate checks preset invariants.
func (p Preset) Validate() error {
	if p.ID == "" {
		return errors.New("missing id")
	}
	if p.Name == "" || len(p.Name) > 255 || !validNameRegex.MatchString(p.Name) {
		return fmt.Errorf("invalid preset name")
	}
	if p.Version < 1 {
		return errors.New("invalid version: must be at least 1")
	}
	if len(p.Config) == 0 {
		return errors.New("empty config")
	}
	if p.UpdatedAt.Before(p.CreatedAt) {
		return errors.New("updated_at must be after or equal to created_at")
	}
	return nil
}
