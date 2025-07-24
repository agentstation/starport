package config

import (
	"testing"
	"time"
)

// validChatUIConfig returns a valid ChatUIConfig for testing
func validChatUIConfig() ChatUIConfig {
	return ChatUIConfig{
		Enabled:     false,
		Title:       "Test Chat",
		Theme:       "light",
		AllowKeyGen: false,
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: &Config{
				Server: ServerConfig{
					Port:            8080,
					Host:            "0.0.0.0",
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					IdleTimeout:     120 * time.Second,
					MaxHeaderBytes:  1048576,
					ShutdownTimeout: 30 * time.Second,
				},
				Storage: StorageConfig{
					Mode: "badger",
					Badger: BadgerConfig{
						Path:           "./data/starport",
						Compression:    "snappy",
						GCInterval:     5 * time.Minute,
						GCDiscardRatio: 0.5,
					},
				},
				RateLimiting: RateLimitingConfig{
					GlobalRequestsPerSecond:  10000,
					GlobalBurstMultiplier:    2.0,
					DefaultRequestsPerMinute: 60,
					DefaultRequestsPerHour:   1000,
					DefaultTokensPerMinute:   100000,
					DefaultTokensPerHour:     1000000,
					WindowSize:               time.Minute,
				},
				Security: SecurityConfig{
					AllowedOrigins: "*",
				},
				Logging: LoggingConfig{
					Level:      "info",
					Format:     "json",
					Output:     "stdout",
					MaxSize:    100,
					MaxBackups: 3,
					MaxAge:     7,
				},
				ChatUI: validChatUIConfig(),
			},
			wantErr: false,
		},
		{
			name: "invalid port",
			config: &Config{
				Server: ServerConfig{
					Port:            70000, // Invalid port
					Host:            "0.0.0.0",
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					IdleTimeout:     120 * time.Second,
					MaxHeaderBytes:  1048576,
					ShutdownTimeout: 30 * time.Second,
				},
				Storage: StorageConfig{
					Mode: "badger",
					Badger: BadgerConfig{
						Path:           "./data/starport",
						Compression:    "snappy",
						GCInterval:     5 * time.Minute,
						GCDiscardRatio: 0.5,
					},
				},
				RateLimiting: RateLimitingConfig{
					GlobalRequestsPerSecond:  10000,
					GlobalBurstMultiplier:    2.0,
					DefaultRequestsPerMinute: 60,
					DefaultRequestsPerHour:   1000,
					DefaultTokensPerMinute:   100000,
					DefaultTokensPerHour:     1000000,
					WindowSize:               time.Minute,
				},
				Security: SecurityConfig{
					AllowedOrigins: "*",
				},
				Logging: LoggingConfig{
					Level:      "info",
					Format:     "json",
					Output:     "stdout",
					MaxSize:    100,
					MaxBackups: 3,
					MaxAge:     7,
				},
				ChatUI: validChatUIConfig(),
			},
			wantErr: true,
			errMsg:  "invalid port number",
		},
		{
			name: "invalid storage mode",
			config: &Config{
				Server: ServerConfig{
					Port:            8080,
					Host:            "0.0.0.0",
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					IdleTimeout:     120 * time.Second,
					MaxHeaderBytes:  1048576,
					ShutdownTimeout: 30 * time.Second,
				},
				Storage: StorageConfig{
					Mode: "invalid", // Invalid storage mode
				},
				RateLimiting: RateLimitingConfig{
					GlobalRequestsPerSecond:  10000,
					GlobalBurstMultiplier:    2.0,
					DefaultRequestsPerMinute: 60,
					DefaultRequestsPerHour:   1000,
					DefaultTokensPerMinute:   100000,
					DefaultTokensPerHour:     1000000,
					WindowSize:               time.Minute,
				},
				Security: SecurityConfig{
					AllowedOrigins: "*",
				},
				Logging: LoggingConfig{
					Level:      "info",
					Format:     "json",
					Output:     "stdout",
					MaxSize:    100,
					MaxBackups: 3,
					MaxAge:     7,
				},
				ChatUI: validChatUIConfig(),
			},
			wantErr: true,
			errMsg:  "unsupported storage mode",
		},
		{
			name: "invalid log level",
			config: &Config{
				Server: ServerConfig{
					Port:            8080,
					Host:            "0.0.0.0",
					ReadTimeout:     30 * time.Second,
					WriteTimeout:    30 * time.Second,
					IdleTimeout:     120 * time.Second,
					MaxHeaderBytes:  1048576,
					ShutdownTimeout: 30 * time.Second,
				},
				Storage: StorageConfig{
					Mode: "badger",
					Badger: BadgerConfig{
						Path:           "./data/starport",
						Compression:    "snappy",
						GCInterval:     5 * time.Minute,
						GCDiscardRatio: 0.5,
					},
				},
				RateLimiting: RateLimitingConfig{
					GlobalRequestsPerSecond:  10000,
					GlobalBurstMultiplier:    2.0,
					DefaultRequestsPerMinute: 60,
					DefaultRequestsPerHour:   1000,
					DefaultTokensPerMinute:   100000,
					DefaultTokensPerHour:     1000000,
					WindowSize:               time.Minute,
				},
				Security: SecurityConfig{
					AllowedOrigins: "*",
				},
				Logging: LoggingConfig{
					Level:      "invalid", // Invalid log level
					Format:     "json",
					Output:     "stdout",
					MaxSize:    100,
					MaxBackups: 3,
					MaxAge:     7,
				},
				ChatUI: validChatUIConfig(),
			},
			wantErr: true,
			errMsg:  "invalid log level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
				t.Errorf("Config.Validate() error = %v, want error containing %v", err, tt.errMsg)
			}
		})
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr ||
		len(s) > len(substr) && containsString(s[1:], substr)
}
