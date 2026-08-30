package config

import (
	"fmt"
	"strconv"
	"strings"
)

// GuardrailsConfig names the checks the deployment's guardrail pipeline
// runs, in order, and what the built-in checks read. Guardrails stay off
// until a check is configured, and an unconfigured deployment adds no
// cost on the request path.
type GuardrailsConfig struct {
	// Checks is a comma-separated list of registered check names, run in
	// the order written. A name no build registers is a startup error.
	// Empty keeps guardrails off.
	Checks string `env:"CHECKS"`
	// PIIMode picks what a PII finding does: redact or refuse. Empty
	// redacts.
	PIIMode string `env:"PII_MODE"`
	// ModerationModel names the catalog moderation model the moderation
	// check calls through the account's own routing. Naming the
	// moderation check without a model is a startup error.
	ModerationModel string `env:"MODERATION_MODEL"`
	// ModerationThreshold refuses when any category scores at or above
	// it. Zero takes the built-in default.
	ModerationThreshold float64 `env:"MODERATION_THRESHOLD"`
	// ModerationThresholds overrides the threshold per category, as
	// comma-separated name=score pairs: "violence=0.8,self-harm=0.2".
	ModerationThresholds string `env:"MODERATION_THRESHOLDS"`
}

// CategoryThresholds parses the per-category threshold overrides. A pair
// that does not read as name=score is a startup error, not a silent
// skip. Nil means no override is configured.
func (c *GuardrailsConfig) CategoryThresholds() (map[string]float64, error) {
	if c == nil || strings.TrimSpace(c.ModerationThresholds) == "" {
		return nil, nil
	}
	thresholds := map[string]float64{}
	for _, pair := range strings.Split(c.ModerationThresholds, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, value, found := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		score, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if !found || name == "" || err != nil {
			return nil, fmt.Errorf("moderation threshold %q does not read as name=score", pair)
		}
		thresholds[name] = score
	}
	return thresholds, nil
}

// Names returns the configured check names, trimmed, without empties, in
// order. A nil or unconfigured receiver means guardrails are off.
func (c *GuardrailsConfig) Names() []string {
	if c == nil || strings.TrimSpace(c.Checks) == "" {
		return nil
	}
	parts := strings.Split(c.Checks, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
}
