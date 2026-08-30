package config

import "strings"

// GuardrailsConfig names the checks the deployment's guardrail pipeline
// runs, in order. Guardrails stay off until a check is configured, and an
// unconfigured deployment adds no cost on the request path.
type GuardrailsConfig struct {
	// Checks is a comma-separated list of registered check names, run in
	// the order written. A name no build registers is a startup error.
	// Empty keeps guardrails off.
	Checks string `env:"CHECKS"`
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
