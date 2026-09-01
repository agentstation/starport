package config

import (
	"errors"
	"fmt"
	"strings"
)

// SemanticCacheConfig turns on the semantic_cache layer: the opt-in
// similarity index beside the exact response cache. The layer stays off
// until an operator enables it, and every request still opts in per call,
// so an unconfigured deployment pays nothing for it.
type SemanticCacheConfig struct {
	// Enabled turns the layer on for the deployment. A request still opts
	// in per call with the X-Semantic-Cache header.
	Enabled bool `env:"ENABLED"`
	// Model names the catalog embedding model that embeds the canonical
	// prompt text through the gateway's own embeddings path. Enabling the
	// layer without a model is a startup error.
	Model string `env:"MODEL"`
	// Threshold is the minimum cosine similarity that answers, in (0, 1].
	// Zero takes the built-in default.
	Threshold float64 `env:"THRESHOLD"`
	// MaxEntries bounds the vectors one similarity scope holds. Zero takes
	// the built-in default.
	MaxEntries int `env:"MAX_ENTRIES"`
}

// Validate refuses a semantic cache the gateway could not run: enabling it
// without an embedding model, a threshold outside (0, 1], or a negative
// bound. A disabled section validates as written so a later enable does
// not surprise.
func (c *SemanticCacheConfig) Validate() error {
	if c == nil {
		return nil
	}
	if c.Enabled && strings.TrimSpace(c.Model) == "" {
		return errors.New("semantic cache needs an embedding model")
	}
	if c.Threshold < 0 || c.Threshold > 1 {
		return fmt.Errorf("semantic cache threshold %v is outside (0, 1]", c.Threshold)
	}
	if c.MaxEntries < 0 {
		return fmt.Errorf("semantic cache max entries %d is negative", c.MaxEntries)
	}
	return nil
}
