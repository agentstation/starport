package server

import (
	"testing"

	"github.com/sethvargo/go-envconfig"

	"github.com/agentstation/starport/internal/config"
)

// TestConfigPortDefaultMatchesTheStandardPort keeps the server tag on the
// standard port. A tag holds a literal, so it repeats config.DefaultPort.
func TestConfigPortDefaultMatchesTheStandardPort(t *testing.T) {
	var cfg Config
	if err := envconfig.ProcessWith(t.Context(), &envconfig.Config{Target: &cfg, Lookuper: envconfig.MapLookuper(nil)}); err != nil {
		t.Fatal(err)
	}
	if cfg.Port != config.DefaultPort {
		t.Fatalf("default port = %d, want %d", cfg.Port, config.DefaultPort)
	}
}
