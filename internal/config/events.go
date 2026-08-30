package config

import "strings"

// EventsConfig names the outbound webhook surface. Webhooks stay off
// until an endpoint is configured, per the active-exporter rule: nothing
// pushes out of an unconfigured deployment.
type EventsConfig struct {
	// WebhookURLs is a comma-separated list of receiver endpoints. Every
	// configured endpoint receives every event. Empty keeps webhooks off.
	WebhookURLs string `env:"WEBHOOK_URLS"`
	// WebhookSecret signs each delivery. A receiver verifies the
	// X-Starport-Signature header with it. Empty signs with the empty
	// secret, which authenticates nothing; set it with any endpoint.
	WebhookSecret string `env:"WEBHOOK_SECRET"`
}

// Endpoints returns the configured receiver URLs, trimmed, without
// empties. A nil or unconfigured receiver set means webhooks are off.
func (c *EventsConfig) Endpoints() []string {
	if c == nil || strings.TrimSpace(c.WebhookURLs) == "" {
		return nil
	}
	parts := strings.Split(c.WebhookURLs, ",")
	endpoints := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			endpoints = append(endpoints, trimmed)
		}
	}
	return endpoints
}
