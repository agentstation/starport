package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

const initFormatJSON = "json"

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

func writeInitResult(writer io.Writer, result InitResult, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		// #nosec G117 -- initialization must return the new credential once.
		return encoder.Encode(result)
	}
	if result.ConfigFile == "" {
		_, err := fmt.Fprintf(
			writer,
			"Initialized Starport API key storage.\nGateway API key (shown once): %s\nRun: starport serve\n",
			result.APIKey,
		)
		return err
	}
	_, err := fmt.Fprintf(
		writer,
		"Initialized Starport.\nConfiguration: %s\nData: %s\nGateway API key (shown once): %s\n%s\n",
		result.ConfigFile,
		result.DataDir,
		result.APIKey,
		"Run: starport serve",
	)
	return err
}
