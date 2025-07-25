package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/sethvargo/go-envconfig"
)

// Loader handles configuration loading from multiple sources
type Loader struct {
	envFiles []string
	prefix   string
}

// NewLoader creates a new configuration loader
func NewLoader() *Loader {
	return &Loader{
		envFiles: []string{"local.env", ".env"},
		prefix:   "STARPORT_",
	}
}

// WithEnvFiles sets custom env files to load (in order of precedence)
func (l *Loader) WithEnvFiles(files ...string) *Loader {
	l.envFiles = files
	return l
}

// WithPrefix sets a custom environment variable prefix
func (l *Loader) WithPrefix(prefix string) *Loader {
	l.prefix = prefix
	return l
}

// Load loads configuration from environment variables and .env files
func (l *Loader) Load(ctx context.Context) (*Config, error) {
	// Load .env files in order (first file takes precedence)
	if err := l.loadEnvFiles(); err != nil {
		return nil, fmt.Errorf("failed to load env files: %w", err)
	}

	// Create config with defaults
	cfg := &Config{}

	// Create a custom lookuper that adds our prefix
	lookuper := envconfig.PrefixLookuper(l.prefix, envconfig.OsLookuper())

	// Process environment variables using go-envconfig
	if err := envconfig.ProcessWith(ctx, &envconfig.Config{
		Target:   cfg,
		Lookuper: lookuper,
	}); err != nil {
		return nil, fmt.Errorf("failed to process env config: %w", err)
	}

	// Apply any post-processing
	if err := l.postProcess(cfg); err != nil {
		return nil, fmt.Errorf("failed to post-process config: %w", err)
	}

	// Validate the configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return cfg, nil
}

// loadEnvFiles loads environment files in order of precedence
func (l *Loader) loadEnvFiles() error {
	// Track which env vars were already set
	existingEnv := make(map[string]bool)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) > 0 {
			existingEnv[parts[0]] = true
		}
	}

	// Load env files in order (first file has precedence)
	for _, file := range l.envFiles {
		// Check if file exists
		if _, err := os.Stat(file); os.IsNotExist(err) {
			// Skip non-existent files
			continue
		}

		// Read the env file
		envMap, err := godotenv.Read(file)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", file, err)
		}

		// Set env vars only if not already set
		for key, value := range envMap {
			if !existingEnv[key] {
				if err := os.Setenv(key, value); err != nil {
					return fmt.Errorf("failed to set env var %s: %w", key, err)
				}
				existingEnv[key] = true
			}
		}
	}

	return nil
}

// postProcess applies any post-processing to the configuration
func (l *Loader) postProcess(cfg *Config) error {
	// Set default provider URLs if not specified
	if cfg.Providers.OpenAI.BaseURL == "" {
		cfg.Providers.OpenAI.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Providers.Anthropic.BaseURL == "" {
		cfg.Providers.Anthropic.BaseURL = "https://api.anthropic.com/v1"
	}
	if cfg.Providers.Gemini.BaseURL == "" {
		// Default to US-Central1 regional endpoint for Vertex AI
		// Users should override with their specific project and location
		cfg.Providers.Gemini.BaseURL = "https://us-central1-aiplatform.googleapis.com"
	}
	if cfg.Providers.Groq.BaseURL == "" {
		cfg.Providers.Groq.BaseURL = "https://api.groq.com/openai/v1"
	}
	if cfg.Providers.Mistral.BaseURL == "" {
		cfg.Providers.Mistral.BaseURL = "https://api.mistral.ai/v1"
	}
	if cfg.Providers.Azure.BaseURL == "" {
		// Azure OpenAI requires resource-specific URLs
		// Format: https://{resource-name}.openai.azure.com
		// Users MUST override this with their specific resource URL
		cfg.Providers.Azure.BaseURL = "https://YOUR-RESOURCE-NAME.openai.azure.com"
	}

	// Ensure storage path is absolute
	if cfg.Storage.Mode == "badger" && cfg.Storage.Badger.Path != "" {
		if !filepath.IsAbs(cfg.Storage.Badger.Path) {
			absPath, err := filepath.Abs(cfg.Storage.Badger.Path)
			if err != nil {
				return fmt.Errorf("failed to resolve badger path: %w", err)
			}
			cfg.Storage.Badger.Path = absPath
		}
	}

	// Parse allowed origins
	if cfg.Security.AllowedOrigins == "" {
		cfg.Security.AllowedOrigins = "*"
	}

	// Load master key from environment if specified
	if cfg.Security.MasterKeyEnv != "" {
		// Note: In a real implementation, we'd store the master key securely
		// For now, we just validate it exists
		_ = os.Getenv(cfg.Security.MasterKeyEnv)
	}

	return nil
}

// LoadWithDefaults loads configuration with default settings
func LoadWithDefaults(ctx context.Context) (*Config, error) {
	return NewLoader().Load(ctx)
}

// MustLoad loads configuration and panics on error
func MustLoad(ctx context.Context) *Config {
	cfg, err := LoadWithDefaults(ctx)
	if err != nil {
		panic(fmt.Sprintf("failed to load configuration: %v", err))
	}
	return cfg
}
