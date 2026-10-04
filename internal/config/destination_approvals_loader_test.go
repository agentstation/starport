package config

import (
	"testing"

	"github.com/agentstation/starport/internal/credentials"
)

// TestLoaderKeepsInstallationDefaultApprovals guards the nil contract on
// InferenceDestinationApprovals. The environment decoder materializes every
// nil pointer to a struct unless the field opts out. A materialized empty set
// is an explicit deny-all policy, so a loader-built deployment refused every
// inference destination and recorded a different recovery selection digest.
func TestLoaderKeepsInstallationDefaultApprovals(t *testing.T) {
	var defaults *credentials.DestinationApprovals
	want, err := defaults.RecoverySelectionSHA256()
	if err != nil {
		t.Fatalf("installation defaults digest: %v", err)
	}
	for name, load := range map[string]func(*testing.T) *Config{
		"production": func(t *testing.T) *Config {
			return loadTestConfig(t, nil)
		},
		"development": func(t *testing.T) *Config {
			cfg, err := NewLoader().
				WithPaths(PathsForConfigDir(t.TempDir())).
				WithEnvironment(nil).
				WithEnvFiles().
				LoadDevelopment(t.Context())
			if err != nil {
				t.Fatalf("load development config: %v", err)
			}
			return cfg
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := load(t)
			if cfg.InferenceDestinationApprovals != nil {
				t.Fatal("loader materialized an empty destination approval set; nil must select the installation defaults")
			}
			got, err := cfg.InferenceDestinationApprovals.RecoverySelectionSHA256()
			if err != nil {
				t.Fatalf("loader-built approvals digest: %v", err)
			}
			if got != want {
				t.Fatalf("recovery selection digest = %s, want installation defaults %s", got, want)
			}
		})
	}
}
