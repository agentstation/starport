package config

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestNewHotReloader(t *testing.T) {
	// Create temp config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "rate_limits.yaml")

	rules := RateLimitRules{
		Version: "1.0",
		Rules: map[string]RateLimitRule{
			"test-key": {
				Name:              "Test Rule",
				RequestsPerMinute: 100,
				RequestsPerHour:   1000,
				TokensPerMinute:   100000,
				TokensPerHour:     1000000,
				BurstMultiplier:   2.0,
			},
		},
		Models: map[string]RateLimitRule{
			"gpt-4": {
				Name:              "GPT-4 Limits",
				RequestsPerMinute: 20,
				RequestsPerHour:   500,
				TokensPerMinute:   40000,
				TokensPerHour:     400000,
				BurstMultiplier:   2.0,
			},
		},
	}

	data, err := yaml.Marshal(&rules)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	// Create hot reloader
	reloader, err := NewHotReloader(configPath, 1*time.Second)
	if err != nil {
		t.Fatalf("failed to create hot reloader: %v", err)
	}
	defer reloader.Stop()

	// Verify initial load
	loadedRules := reloader.GetRateLimitRules()
	if loadedRules == nil {
		t.Fatal("expected rules to be loaded")
	}

	if len(loadedRules.Rules) != 1 {
		t.Errorf("expected 1 rule, got %d", len(loadedRules.Rules))
	}

	if len(loadedRules.Models) != 1 {
		t.Errorf("expected 1 model rule, got %d", len(loadedRules.Models))
	}
}

func TestHotReloader_GetRuleForKey(t *testing.T) {
	// Create temp config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "rate_limits.yaml")

	rules := RateLimitRules{
		Version: "1.0",
		Rules: map[string]RateLimitRule{
			"test-key": {
				Name:              "Test Rule",
				RequestsPerMinute: 100,
				RequestsPerHour:   1000,
				TokensPerMinute:   100000,
				TokensPerHour:     1000000,
				BurstMultiplier:   2.0,
			},
		},
	}

	data, err := yaml.Marshal(&rules)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	reloader, err := NewHotReloader(configPath, 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer reloader.Stop()

	// Test existing key
	rule, ok := reloader.GetRuleForKey("test-key")
	if !ok {
		t.Error("expected to find rule for test-key")
	}
	if rule.RequestsPerMinute != 100 {
		t.Errorf("expected requests per minute 100, got %d", rule.RequestsPerMinute)
	}

	// Test non-existent key
	_, ok = reloader.GetRuleForKey("non-existent")
	if ok {
		t.Error("expected not to find rule for non-existent key")
	}
}

func TestHotReloader_GetRuleForModel(t *testing.T) {
	// Create temp config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "rate_limits.yaml")

	rules := RateLimitRules{
		Version: "1.0",
		Models: map[string]RateLimitRule{
			"gpt-4": {
				Name:              "GPT-4 Limits",
				RequestsPerMinute: 20,
				RequestsPerHour:   500,
				TokensPerMinute:   40000,
				TokensPerHour:     400000,
				BurstMultiplier:   2.0,
			},
		},
	}

	data, err := yaml.Marshal(&rules)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	reloader, err := NewHotReloader(configPath, 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer reloader.Stop()

	// Test existing model
	rule, ok := reloader.GetRuleForModel("gpt-4")
	if !ok {
		t.Error("expected to find rule for gpt-4")
	}
	if rule.RequestsPerMinute != 20 {
		t.Errorf("expected requests per minute 20, got %d", rule.RequestsPerMinute)
	}

	// Test non-existent model
	_, ok = reloader.GetRuleForModel("non-existent")
	if ok {
		t.Error("expected not to find rule for non-existent model")
	}
}

func TestHotReloader_HotReload(t *testing.T) {
	// Create temp config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "rate_limits.yaml")

	initialRules := RateLimitRules{
		Version: "1.0",
		Rules: map[string]RateLimitRule{
			"test-key": {
				Name:              "Test Rule",
				RequestsPerMinute: 100,
				RequestsPerHour:   1000,
				TokensPerMinute:   100000,
				TokensPerHour:     1000000,
				BurstMultiplier:   2.0,
			},
		},
	}

	data, err := yaml.Marshal(&initialRules)
	if err != nil {
		t.Fatal(err)
	}

	// Use atomic write to avoid partial reads
	tempPath := configPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tempPath, configPath); err != nil {
		t.Fatal(err)
	}

	reloader, err := NewHotReloader(configPath, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer reloader.Stop()

	// Start hot reload monitoring
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := reloader.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Set up callback to track updates
	var mu sync.Mutex
	updateCount := 0
	var lastUpdate *RateLimitRules
	reloader.OnUpdate(func(rules *RateLimitRules) {
		mu.Lock()
		defer mu.Unlock()
		updateCount++
		lastUpdate = rules
	})

	// Verify initial state
	rule, ok := reloader.GetRuleForKey("test-key")
	if !ok || rule.RequestsPerMinute != 100 {
		t.Error("initial state incorrect")
	}

	// Update config file
	updatedRules := RateLimitRules{
		Version: "1.0",
		Rules: map[string]RateLimitRule{
			"test-key": {
				Name:              "Updated Rule",
				RequestsPerMinute: 200, // Changed
				RequestsPerHour:   2000, // Changed
				TokensPerMinute:   200000,
				TokensPerHour:     2000000,
				BurstMultiplier:   3.0,
			},
		},
	}

	data, err = yaml.Marshal(&updatedRules)
	if err != nil {
		t.Fatal(err)
	}

	// Write update - use atomic write to avoid partial reads
	tempPath = configPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tempPath, configPath); err != nil {
		t.Fatal(err)
	}

	// Wait for reload - give extra time for file watcher
	time.Sleep(800 * time.Millisecond)

	// Verify update
	rule, ok = reloader.GetRuleForKey("test-key")
	if !ok {
		t.Error("expected to find updated rule")
	}
	if rule.RequestsPerMinute != 200 {
		t.Errorf("expected updated requests per minute 200, got %d", rule.RequestsPerMinute)
	}

	// Verify callback was called
	mu.Lock()
	finalUpdateCount := updateCount
	finalLastUpdate := lastUpdate
	mu.Unlock()
	
	if finalUpdateCount == 0 {
		t.Error("expected update callback to be called")
	}
	if finalLastUpdate == nil || len(finalLastUpdate.Rules) != 1 {
		t.Error("callback received incorrect data")
	}
}

func TestHotReloader_ValidateRules(t *testing.T) {
	reloader := &HotReloader{}

	tests := []struct {
		name    string
		rules   *RateLimitRules
		wantErr bool
	}{
		{
			name: "valid rules",
			rules: &RateLimitRules{
				Version: "1.0",
				Rules: map[string]RateLimitRule{
					"test": {
						Name:              "Test",
						RequestsPerMinute: 100,
						RequestsPerHour:   1000,
						TokensPerMinute:   100000,
						TokensPerHour:     1000000,
						BurstMultiplier:   2.0,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty rule name",
			rules: &RateLimitRules{
				Version: "1.0",
				Rules: map[string]RateLimitRule{
					"": {
						RequestsPerMinute: 100,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "negative requests per minute",
			rules: &RateLimitRules{
				Version: "1.0",
				Rules: map[string]RateLimitRule{
					"test": {
						RequestsPerMinute: -1,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "negative burst multiplier",
			rules: &RateLimitRules{
				Version: "1.0",
				Rules: map[string]RateLimitRule{
					"test": {
						RequestsPerMinute: 100,
						BurstMultiplier:   -1.0,
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := reloader.validateRules(tt.rules)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateRules() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHotReloader_NonExistentFile(t *testing.T) {
	// Create temp directory but not the file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "non_existent.yaml")

	reloader, err := NewHotReloader(configPath, 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer reloader.Stop()

	// Should have empty rules
	rules := reloader.GetRateLimitRules()
	if rules == nil {
		t.Error("expected empty rules, got nil")
	}
	if len(rules.Rules) != 0 || len(rules.Models) != 0 {
		t.Error("expected empty rules")
	}
}