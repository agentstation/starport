package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/agentstation/starmap/pkg/catalogs"
)

const initFormatJSON = "json"

// InitOptions contains explicit local initialization choices.
type InitOptions struct {
	Provider          catalogs.ProviderID
	IdentityName      string
	ConfiguredStorage bool
}

// InitResult contains initialized paths and the one-time gateway credential.
type InitResult struct {
	Provider     catalogs.ProviderID `json:"provider,omitempty"`
	IdentityName string              `json:"identity_name"`
	ConfigFile   string              `json:"config_file,omitempty"`
	DataDir      string              `json:"data_dir,omitempty"`
	APIKey       string              `json:"api_key"`
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
			"Initialized Starport identity storage.\nGateway API key (shown once): %s\nRun: starport serve\n",
			result.APIKey,
		)
		return err
	}
	next := "Run: starport serve"
	if result.Provider == catalogs.ProviderIDOllama {
		next = "Next: add installed Ollama models to a reviewed Starmap workspace, then run starport serve"
	}
	_, err := fmt.Fprintf(
		writer,
		"Initialized Starport for %s.\nConfiguration: %s\nData: %s\nGateway API key (shown once): %s\n%s\n",
		result.Provider,
		result.ConfigFile,
		result.DataDir,
		result.APIKey,
		next,
	)
	return err
}
