package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  ServerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: ServerConfig{
				Port:           8080,
				Host:           "0.0.0.0",
				ReadTimeout:    30 * time.Second,
				WriteTimeout:   30 * time.Second,
				IdleTimeout:    120 * time.Second,
				MaxHeaderBytes: 1048576,
			},
			wantErr: false,
		},
		{
			name: "invalid port - too low",
			config: ServerConfig{
				Port:           0,
				Host:           "0.0.0.0",
				ReadTimeout:    30 * time.Second,
				WriteTimeout:   30 * time.Second,
				IdleTimeout:    120 * time.Second,
				MaxHeaderBytes: 1048576,
			},
			wantErr: true,
		},
		{
			name: "invalid port - too high",
			config: ServerConfig{
				Port:           70000,
				Host:           "0.0.0.0",
				ReadTimeout:    30 * time.Second,
				WriteTimeout:   30 * time.Second,
				IdleTimeout:    120 * time.Second,
				MaxHeaderBytes: 1048576,
			},
			wantErr: true,
		},
		{
			name: "localhost is valid",
			config: ServerConfig{
				Port:           8080,
				Host:           "localhost",
				ReadTimeout:    30 * time.Second,
				WriteTimeout:   30 * time.Second,
				IdleTimeout:    120 * time.Second,
				MaxHeaderBytes: 1048576,
			},
			wantErr: false,
		},
		{
			name: "invalid timeout",
			config: ServerConfig{
				Port:           8080,
				Host:           "0.0.0.0",
				ReadTimeout:    0,
				WriteTimeout:   30 * time.Second,
				IdleTimeout:    120 * time.Second,
				MaxHeaderBytes: 1048576,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("ServerConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBadgerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  BadgerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: BadgerConfig{
				Path:           "./data/starport",
				Compression:    "snappy",
				GCInterval:     5 * time.Minute,
				GCDiscardRatio: 0.5,
			},
			wantErr: false,
		},
		{
			name: "empty path",
			config: BadgerConfig{
				Path:           "",
				Compression:    "snappy",
				GCInterval:     5 * time.Minute,
				GCDiscardRatio: 0.5,
			},
			wantErr: true,
		},
		{
			name: "invalid compression",
			config: BadgerConfig{
				Path:           "./data/starport",
				Compression:    "invalid",
				GCInterval:     5 * time.Minute,
				GCDiscardRatio: 0.5,
			},
			wantErr: true,
		},
		{
			name: "valid compression - none",
			config: BadgerConfig{
				Path:           "./data/starport",
				Compression:    "none",
				GCInterval:     5 * time.Minute,
				GCDiscardRatio: 0.5,
			},
			wantErr: false,
		},
		{
			name: "valid compression - zstd",
			config: BadgerConfig{
				Path:           "./data/starport",
				Compression:    "zstd",
				GCInterval:     5 * time.Minute,
				GCDiscardRatio: 0.5,
			},
			wantErr: false,
		},
		{
			name: "invalid GC discard ratio",
			config: BadgerConfig{
				Path:           "./data/starport",
				Compression:    "snappy",
				GCInterval:     5 * time.Minute,
				GCDiscardRatio: 1.5,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("BadgerConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValkeyConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  ValkeyConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: ValkeyConfig{
				URL:            "valkey://localhost:6379",
				MaxConnections: 50,
				MinIdleConns:   10,
			},
			wantErr: false,
		},
		{
			name: "valid config - redis scheme",
			config: ValkeyConfig{
				URL:            "redis://localhost:6379",
				MaxConnections: 50,
				MinIdleConns:   10,
			},
			wantErr: false,
		},
		{
			name: "valid config - rediss scheme",
			config: ValkeyConfig{
				URL:            "rediss://localhost:6379",
				MaxConnections: 50,
				MinIdleConns:   10,
			},
			wantErr: false,
		},
		{
			name: "empty URL",
			config: ValkeyConfig{
				URL:            "",
				MaxConnections: 50,
				MinIdleConns:   10,
			},
			wantErr: true,
		},
		{
			name: "invalid URL scheme",
			config: ValkeyConfig{
				URL:            "http://localhost:6379",
				MaxConnections: 50,
				MinIdleConns:   10,
			},
			wantErr: true,
		},
		{
			name: "invalid max connections",
			config: ValkeyConfig{
				URL:            "valkey://localhost:6379",
				MaxConnections: 0,
				MinIdleConns:   10,
			},
			wantErr: true,
		},
		{
			name: "min idle exceeds max",
			config: ValkeyConfig{
				URL:            "valkey://localhost:6379",
				MaxConnections: 50,
				MinIdleConns:   100,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("ValkeyConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSecurityConfig_Validate(t *testing.T) {
	// Create temp cert and key files for testing
	tempDir := t.TempDir()
	certPath := filepath.Join(tempDir, "cert.pem")
	keyPath := filepath.Join(tempDir, "key.pem")
	os.WriteFile(certPath, []byte("cert"), 0644)
	os.WriteFile(keyPath, []byte("key"), 0644)

	tests := []struct {
		name    string
		config  SecurityConfig
		wantErr bool
	}{
		{
			name: "valid config without TLS",
			config: SecurityConfig{
				EnableTLS:      false,
				AllowedOrigins: "*",
			},
			wantErr: false,
		},
		{
			name: "bootstrap API key is too short",
			config: SecurityConfig{
				BootstrapAPIKey: "short",
				AllowedOrigins:  "*",
			},
			wantErr: true,
		},
		{
			name: "valid config with TLS",
			config: SecurityConfig{
				EnableTLS:      true,
				TLSCertPath:    certPath,
				TLSKeyPath:     keyPath,
				AllowedOrigins: "*",
			},
			wantErr: false,
		},
		{
			name: "TLS enabled without cert",
			config: SecurityConfig{
				EnableTLS:      true,
				TLSCertPath:    "",
				TLSKeyPath:     keyPath,
				AllowedOrigins: "*",
			},
			wantErr: true,
		},
		{
			name: "TLS enabled without key",
			config: SecurityConfig{
				EnableTLS:      true,
				TLSCertPath:    certPath,
				TLSKeyPath:     "",
				AllowedOrigins: "*",
			},
			wantErr: true,
		},
		{
			name: "TLS enabled with non-existent files",
			config: SecurityConfig{
				EnableTLS:      true,
				TLSCertPath:    "/non/existent/cert.pem",
				TLSKeyPath:     "/non/existent/key.pem",
				AllowedOrigins: "*",
			},
			wantErr: true,
		},
		{
			name: "valid specific origins",
			config: SecurityConfig{
				EnableTLS:      false,
				AllowedOrigins: "https://example.com, http://localhost:3000",
			},
			wantErr: false,
		},
		{
			name: "invalid origin format",
			config: SecurityConfig{
				EnableTLS:      false,
				AllowedOrigins: "example.com",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("SecurityConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoggingConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  LoggingConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: LoggingConfig{
				Level:      "info",
				Format:     "json",
				Output:     "stdout",
				MaxSize:    100,
				MaxBackups: 3,
				MaxAge:     7,
			},
			wantErr: false,
		},
		{
			name: "valid log levels",
			config: LoggingConfig{
				Level:      "debug",
				Format:     "json",
				Output:     "stdout",
				MaxSize:    100,
				MaxBackups: 3,
				MaxAge:     7,
			},
			wantErr: false,
		},
		{
			name: "invalid log level",
			config: LoggingConfig{
				Level:      "invalid",
				Format:     "json",
				Output:     "stdout",
				MaxSize:    100,
				MaxBackups: 3,
				MaxAge:     7,
			},
			wantErr: true,
		},
		{
			name: "invalid format",
			config: LoggingConfig{
				Level:      "info",
				Format:     "xml",
				Output:     "stdout",
				MaxSize:    100,
				MaxBackups: 3,
				MaxAge:     7,
			},
			wantErr: true,
		},
		{
			name: "file output without path",
			config: LoggingConfig{
				Level:      "info",
				Format:     "json",
				Output:     "file",
				FilePath:   "",
				MaxSize:    100,
				MaxBackups: 3,
				MaxAge:     7,
			},
			wantErr: true,
		},
		{
			name: "file output with path",
			config: LoggingConfig{
				Level:      "info",
				Format:     "json",
				Output:     "file",
				FilePath:   "/var/log/starport.log",
				MaxSize:    100,
				MaxBackups: 3,
				MaxAge:     7,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("LoggingConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestChatUIConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  ChatUIConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: ChatUIConfig{
				Enabled: true,
				Title:   "Starport Chat",
				Theme:   "light",
			},
			wantErr: false,
		},
		{
			name: "valid dark theme",
			config: ChatUIConfig{
				Enabled: true,
				Title:   "Starport Chat",
				Theme:   "dark",
			},
			wantErr: false,
		},
		{
			name: "invalid theme",
			config: ChatUIConfig{
				Enabled: true,
				Title:   "Starport Chat",
				Theme:   "purple",
			},
			wantErr: true,
			errMsg:  "invalid theme: purple (must be 'light' or 'dark')",
		},
		{
			name: "empty title",
			config: ChatUIConfig{
				Enabled: true,
				Title:   "",
				Theme:   "light",
			},
			wantErr: true,
			errMsg:  "ChatUI title cannot be empty",
		},
		{
			name: "disabled config still validates",
			config: ChatUIConfig{
				Enabled: false,
				Title:   "Test",
				Theme:   "light",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && err.Error() != tt.errMsg {
				t.Errorf("Validate() error message = %v, want %v", err.Error(), tt.errMsg)
			}
		})
	}
}
