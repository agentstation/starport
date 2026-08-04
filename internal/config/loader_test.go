package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoaderUsesExactProviderEnvironmentNamespaces(t *testing.T) {
	t.Run("exact names", func(t *testing.T) {
		t.Setenv("STARPORT_PROVIDERS_GOOGLE_AI_STUDIO_API_KEY", "studio-key")
		t.Setenv("STARPORT_PROVIDERS_GOOGLE_VERTEX_API_KEY", "vertex-token")
		t.Setenv("STARPORT_PROVIDERS_GOOGLE_VERTEX_PROJECT_ID", "vertex-project")
		t.Setenv("STARPORT_PROVIDERS_AZURE_OPENAI_API_KEY", "azure-key")

		cfg, err := NewLoader().Load(context.Background())
		if err != nil {
			t.Fatalf("load exact provider namespaces: %v", err)
		}
		if cfg.Providers.GoogleAIStudio.APIKey != "studio-key" {
			t.Fatalf("Google AI Studio API key = %q", cfg.Providers.GoogleAIStudio.APIKey)
		}
		if cfg.Providers.GoogleVertexAI.APIKey != "vertex-token" {
			t.Fatalf("Google Vertex token = %q", cfg.Providers.GoogleVertexAI.APIKey)
		}
		if cfg.Providers.GoogleVertexAI.ProjectID != "vertex-project" {
			t.Fatalf("Google Vertex project = %q", cfg.Providers.GoogleVertexAI.ProjectID)
		}
		if cfg.Providers.Azure.APIKey != "azure-key" {
			t.Fatalf("Azure OpenAI API key = %q", cfg.Providers.Azure.APIKey)
		}
	})

	t.Run("old names are ignored", func(t *testing.T) {
		t.Setenv("GOOGLE_API_KEY", "")
		t.Setenv("AZURE_OPENAI_API_KEY", "")
		t.Setenv("STARPORT_PROVIDERS_GOOGLE_AISTUDIO_API_KEY", "old-studio-key")
		t.Setenv("STARPORT_PROVIDERS_GOOGLE_VERTEXAI_API_KEY", "old-vertex-token")
		t.Setenv("STARPORT_PROVIDERS_AZURE_API_KEY", "old-azure-key")

		cfg, err := NewLoader().Load(context.Background())
		if err != nil {
			t.Fatalf("load old provider namespaces: %v", err)
		}
		if cfg.Providers.GoogleAIStudio.APIKey != "" {
			t.Fatalf("old Google AI Studio namespace was accepted")
		}
		if cfg.Providers.GoogleVertexAI.APIKey != "" {
			t.Fatalf("old Google Vertex namespace was accepted")
		}
		if cfg.Providers.Azure.APIKey != "" {
			t.Fatalf("old Azure OpenAI namespace was accepted")
		}
	})
}

func TestLoader_Load(t *testing.T) {
	// Save current environment
	originalEnv := make(map[string]string)
	envVars := []string{
		"STARPORT_SERVER_PORT",
		"STARPORT_STORAGE_MODE",
		"STARPORT_LOGGING_LEVEL",
	}
	for _, key := range envVars {
		originalEnv[key] = os.Getenv(key)
	}

	// Restore environment after test
	defer func() {
		for key, value := range originalEnv {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	tests := []struct {
		name    string
		setup   func()
		wantErr bool
		verify  func(t *testing.T, cfg *Config)
	}{
		{
			name: "default configuration",
			setup: func() {
				// Clear test env vars
				for _, key := range envVars {
					os.Unsetenv(key)
				}
			},
			wantErr: false,
			verify: func(t *testing.T, cfg *Config) {
				if cfg.Server.Port != 8080 {
					t.Errorf("expected default port 8080, got %d", cfg.Server.Port)
				}
				if cfg.Storage.Mode != "badger" {
					t.Errorf("expected default storage mode 'badger', got %s", cfg.Storage.Mode)
				}
				if cfg.Logging.Level != "info" {
					t.Errorf("expected default log level 'info', got %s", cfg.Logging.Level)
				}
			},
		},
		{
			name: "environment override",
			setup: func() {
				os.Setenv("STARPORT_SERVER_PORT", "9090")
				os.Setenv("STARPORT_STORAGE_MODE", "valkey")
				os.Setenv("STARPORT_LOGGING_LEVEL", "debug")
			},
			wantErr: false,
			verify: func(t *testing.T, cfg *Config) {
				if cfg.Server.Port != 9090 {
					t.Errorf("expected port 9090, got %d", cfg.Server.Port)
				}
				if cfg.Storage.Mode != "valkey" {
					t.Errorf("expected storage mode 'valkey', got %s", cfg.Storage.Mode)
				}
				if cfg.Logging.Level != "debug" {
					t.Errorf("expected log level 'debug', got %s", cfg.Logging.Level)
				}
			},
		},
		{
			name: "invalid configuration",
			setup: func() {
				os.Setenv("STARPORT_SERVER_PORT", "invalid")
			},
			wantErr: true,
			verify:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			loader := NewLoader()
			cfg, err := loader.Load(context.Background())

			if (err != nil) != tt.wantErr {
				t.Errorf("Loader.Load() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr && tt.verify != nil {
				tt.verify(t, cfg)
			}
		})
	}
}

func TestLoader_LoadEnvFiles(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create test .env file
	envContent := `STARPORT_SERVER_PORT=8888
STARPORT_LOGGING_LEVEL=warn`
	envFile := filepath.Join(tempDir, ".env")
	if err := os.WriteFile(envFile, []byte(envContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create test local.env file (should take precedence)
	localEnvContent := `STARPORT_SERVER_PORT=7777`
	localEnvFile := filepath.Join(tempDir, "local.env")
	if err := os.WriteFile(localEnvFile, []byte(localEnvContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Save and restore original env
	originalPort := os.Getenv("STARPORT_SERVER_PORT")
	originalLevel := os.Getenv("STARPORT_LOGGING_LEVEL")
	defer func() {
		if originalPort == "" {
			os.Unsetenv("STARPORT_SERVER_PORT")
		} else {
			os.Setenv("STARPORT_SERVER_PORT", originalPort)
		}
		if originalLevel == "" {
			os.Unsetenv("STARPORT_LOGGING_LEVEL")
		} else {
			os.Setenv("STARPORT_LOGGING_LEVEL", originalLevel)
		}
	}()

	// Clear env vars
	os.Unsetenv("STARPORT_SERVER_PORT")
	os.Unsetenv("STARPORT_LOGGING_LEVEL")

	// Change to temp directory for test
	originalWd, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(originalWd)

	// Load configuration
	loader := NewLoader()
	cfg, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify local.env takes precedence for port
	if cfg.Server.Port != 7777 {
		t.Errorf("expected port 7777 from local.env, got %d", cfg.Server.Port)
	}

	// Verify .env is loaded for other values
	if cfg.Logging.Level != "warn" {
		t.Errorf("expected log level 'warn' from .env, got %s", cfg.Logging.Level)
	}
}

func TestLoader_PostProcess(t *testing.T) {
	loader := NewLoader()

	cfg := &Config{
		Providers: ProvidersConfig{
			OpenAI:    ProviderConfig{},
			Anthropic: ProviderConfig{},
		},
		Storage: StorageConfig{
			Mode: "badger",
			Badger: BadgerConfig{
				Path: "relative/path",
			},
		},
		Security: SecurityConfig{},
	}

	err := loader.postProcess(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Provider service defaults come from Starmap during adapter activation.
	for _, entry := range cfg.Providers.Entries() {
		if entry.Config.BaseURL != "" {
			t.Errorf("expected %s base URL to remain unset, got %s", entry.ProviderID, entry.Config.BaseURL)
		}
	}

	// Verify path is made absolute
	if !filepath.IsAbs(cfg.Storage.Badger.Path) {
		t.Errorf("expected badger path to be absolute, got %s", cfg.Storage.Badger.Path)
	}

	// Verify default allowed origins
	if cfg.Security.AllowedOrigins != "*" {
		t.Errorf("expected default allowed origins '*', got %s", cfg.Security.AllowedOrigins)
	}
}
