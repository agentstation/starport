package config

import "testing"

func TestSemanticCacheConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		config  SemanticCacheConfig
		wantErr bool
	}{
		{name: "off and empty", config: SemanticCacheConfig{}},
		{name: "enabled with model", config: SemanticCacheConfig{Enabled: true, Model: "openai/text-embedding-3-small"}},
		{name: "enabled with bounds", config: SemanticCacheConfig{Enabled: true, Model: "openai/text-embedding-3-small", Threshold: 0.97, MaxEntries: 64}},
		{name: "enabled without model", config: SemanticCacheConfig{Enabled: true}, wantErr: true},
		{name: "threshold above one", config: SemanticCacheConfig{Enabled: true, Model: "m", Threshold: 1.5}, wantErr: true},
		{name: "negative threshold", config: SemanticCacheConfig{Threshold: -0.1}, wantErr: true},
		{name: "negative bound", config: SemanticCacheConfig{MaxEntries: -1}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected a validation error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
