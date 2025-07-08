package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// RateLimitRule represents a rate limit configuration that can be hot-reloaded
type RateLimitRule struct {
	Name             string `yaml:"name"`
	RequestsPerMinute int    `yaml:"requests_per_minute"`
	RequestsPerHour   int    `yaml:"requests_per_hour"`
	TokensPerMinute   int    `yaml:"tokens_per_minute"`
	TokensPerHour     int    `yaml:"tokens_per_hour"`
	BurstMultiplier   float64 `yaml:"burst_multiplier"`
}

// RateLimitRules represents the hot-reloadable rate limit configuration
type RateLimitRules struct {
	Version string                    `yaml:"version"`
	Rules   map[string]RateLimitRule  `yaml:"rules"`
	Models  map[string]RateLimitRule  `yaml:"models"`
}

// HotReloader manages hot-reloading of configuration files
type HotReloader struct {
	mu              sync.RWMutex
	configPath      string
	checkInterval   time.Duration
	rateLimitRules  *RateLimitRules
	watcher         *fsnotify.Watcher
	updateCallbacks []func(*RateLimitRules)
	stopCh          chan struct{}
	lastModTime     time.Time
}

// NewHotReloader creates a new hot reloader
func NewHotReloader(configPath string, checkInterval time.Duration) (*HotReloader, error) {
	h := &HotReloader{
		configPath:      configPath,
		checkInterval:   checkInterval,
		updateCallbacks: make([]func(*RateLimitRules), 0),
		stopCh:          make(chan struct{}),
	}

	// Load initial configuration
	if err := h.loadConfig(); err != nil {
		return nil, fmt.Errorf("failed to load initial config: %w", err)
	}

	// Create file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}
	h.watcher = watcher

	// Watch the config file if it exists
	if _, err := os.Stat(configPath); err == nil {
		if err := watcher.Add(configPath); err != nil {
			_ = watcher.Close()
			return nil, fmt.Errorf("failed to watch config file: %w", err)
		}
	} else {
		// Watch the directory instead
		dir := filepath.Dir(configPath)
		if err := watcher.Add(dir); err != nil {
			_ = watcher.Close()
			// If we can't watch, just continue without file watching
			h.watcher = nil
		}
	}

	return h, nil
}

// Start begins the hot reload monitoring
func (h *HotReloader) Start(ctx context.Context) error {
	// Start file watcher
	go h.watchLoop(ctx)

	// Start periodic check as backup
	go h.periodicCheckLoop(ctx)

	return nil
}

// Stop stops the hot reloader
func (h *HotReloader) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	select {
	case <-h.stopCh:
		// Already stopped
		return
	default:
		close(h.stopCh)
		if h.watcher != nil {
			_ = h.watcher.Close()
		}
	}
}

// OnUpdate registers a callback to be called when configuration is updated
func (h *HotReloader) OnUpdate(callback func(*RateLimitRules)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.updateCallbacks = append(h.updateCallbacks, callback)
}

// GetRateLimitRules returns the current rate limit rules
func (h *HotReloader) GetRateLimitRules() *RateLimitRules {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.rateLimitRules
}

// GetRuleForKey returns the rate limit rule for a specific API key
func (h *HotReloader) GetRuleForKey(keyID string) (*RateLimitRule, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	if h.rateLimitRules == nil {
		return nil, false
	}
	
	rule, ok := h.rateLimitRules.Rules[keyID]
	return &rule, ok
}

// GetRuleForModel returns the rate limit rule for a specific model
func (h *HotReloader) GetRuleForModel(model string) (*RateLimitRule, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	if h.rateLimitRules == nil {
		return nil, false
	}
	
	rule, ok := h.rateLimitRules.Models[model]
	return &rule, ok
}

// loadConfig loads the configuration from disk
func (h *HotReloader) loadConfig() error {
	// Check if file exists
	info, err := os.Stat(h.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, use empty rules
			h.mu.Lock()
			h.rateLimitRules = &RateLimitRules{
				Version: "1.0",
				Rules:   make(map[string]RateLimitRule),
				Models:  make(map[string]RateLimitRule),
			}
			h.mu.Unlock()
			return nil
		}
		return fmt.Errorf("failed to stat config file: %w", err)
	}

	// Check if file was modified
	h.mu.Lock()
	lastMod := h.lastModTime
	h.mu.Unlock()
	
	if !lastMod.IsZero() && info.ModTime().Equal(lastMod) {
		// File hasn't changed
		return nil
	}

	// Read the file
	data, err := os.ReadFile(h.configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Check for empty file
	if len(data) == 0 {
		return fmt.Errorf("config file is empty")
	}

	// Parse YAML
	var rules RateLimitRules
	if err := yaml.Unmarshal(data, &rules); err != nil {
		// Check if it's a partial file read
		if len(data) < 10 || strings.TrimSpace(string(data)) == "" {
			return fmt.Errorf("config file appears incomplete or empty")
		}
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	// Initialize maps if nil
	if rules.Rules == nil {
		rules.Rules = make(map[string]RateLimitRule)
	}
	if rules.Models == nil {
		rules.Models = make(map[string]RateLimitRule)
	}

	// Validate rules
	if err := h.validateRules(&rules); err != nil {
		return fmt.Errorf("invalid rate limit rules: %w", err)
	}

	// Update configuration
	h.mu.Lock()
	h.rateLimitRules = &rules
	h.lastModTime = info.ModTime()
	h.mu.Unlock()

	// Notify callbacks
	h.notifyCallbacks(&rules)

	log.Info().
		Str("config_path", h.configPath).
		Int("rules_count", len(rules.Rules)).
		Int("models_count", len(rules.Models)).
		Msg("Loaded rate limit configuration")

	return nil
}

// validateRules validates rate limit rules
func (h *HotReloader) validateRules(rules *RateLimitRules) error {
	// Validate each rule
	for name, rule := range rules.Rules {
		if name == "" {
			return fmt.Errorf("empty rule name")
		}
		if err := h.validateRule(&rule); err != nil {
			return fmt.Errorf("invalid rule %s: %w", name, err)
		}
	}

	// Validate each model rule
	for model, rule := range rules.Models {
		if model == "" {
			return fmt.Errorf("empty model name")
		}
		if err := h.validateRule(&rule); err != nil {
			return fmt.Errorf("invalid model rule %s: %w", model, err)
		}
	}

	return nil
}

// validateRule validates a single rate limit rule
func (h *HotReloader) validateRule(rule *RateLimitRule) error {
	if rule.RequestsPerMinute < 0 {
		return fmt.Errorf("requests per minute cannot be negative")
	}
	if rule.RequestsPerHour < 0 {
		return fmt.Errorf("requests per hour cannot be negative")
	}
	if rule.TokensPerMinute < 0 {
		return fmt.Errorf("tokens per minute cannot be negative")
	}
	if rule.TokensPerHour < 0 {
		return fmt.Errorf("tokens per hour cannot be negative")
	}
	if rule.BurstMultiplier < 0 {
		return fmt.Errorf("burst multiplier cannot be negative")
	}
	return nil
}

// watchLoop monitors file changes
func (h *HotReloader) watchLoop(ctx context.Context) {
	if h.watcher == nil {
		return
	}
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stopCh:
			return
		case event, ok := <-h.watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create {
				// Check if it's our config file
				if event.Name == h.configPath || filepath.Base(event.Name) == filepath.Base(h.configPath) {
					log.Info().
						Str("file", event.Name).
						Str("op", event.Op.String()).
						Msg("Config file changed, reloading")
					
					// Small delay to ensure file write is complete
					time.Sleep(100 * time.Millisecond)
					
					// Retry logic for file reads
					var err error
					for i := 0; i < 3; i++ {
						err = h.loadConfig()
						if err == nil {
							break
						}
						if i < 2 {
							time.Sleep(50 * time.Millisecond)
						}
					}
					
					if err != nil {
						log.Error().Err(err).Msg("Failed to reload config after retries")
					}
				}
			}
		case err, ok := <-h.watcher.Errors:
			if !ok {
				return
			}
			log.Error().Err(err).Msg("File watcher error")
		}
	}
}

// periodicCheckLoop periodically checks for config changes as a backup
func (h *HotReloader) periodicCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(h.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stopCh:
			return
		case <-ticker.C:
			if err := h.loadConfig(); err != nil {
				log.Error().Err(err).Msg("Failed to reload config during periodic check")
			}
		}
	}
}

// notifyCallbacks notifies all registered callbacks of configuration update
func (h *HotReloader) notifyCallbacks(rules *RateLimitRules) {
	h.mu.RLock()
	callbacks := make([]func(*RateLimitRules), len(h.updateCallbacks))
	copy(callbacks, h.updateCallbacks)
	h.mu.RUnlock()

	for _, callback := range callbacks {
		callback(rules)
	}
}