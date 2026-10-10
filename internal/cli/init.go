package cli

import "context"

const initFormatJSON = "json"

// DefaultAPIKeyName names the first gateway API key of a local root.
const DefaultAPIKeyName = "local-admin"

// InitOptions contains explicit local initialization choices.
type InitOptions struct {
	APIKeyName        string
	ConfiguredStorage bool
}

// InitResult contains initialized paths and the one-time gateway credential.
type InitResult struct {
	APIKeyName string                      `json:"api_key_name"`
	ConfigFile string                      `json:"config_file,omitempty"`
	DataDir    string                      `json:"data_dir,omitempty"`
	APIKey     string                      `json:"api_key"`
	Rollback   func(context.Context) error `json:"-"`
}
