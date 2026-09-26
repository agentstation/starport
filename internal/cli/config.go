package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/agentstation/starport/internal/config"
)

const configFormatJSON = "json"

type validationResult struct {
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

func writeConfiguration(writer io.Writer, cfg *config.Config, asJSON bool) error {
	view := config.Redacted(cfg)
	if asJSON {
		return writeIndentedJSON(writer, view)
	}
	lines := make([]string, 0)
	flattenConfiguration("", view, &lines)
	sort.Strings(lines)
	for _, line := range lines {
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	return nil
}

func writePaths(writer io.Writer, paths config.Paths, asJSON bool) error {
	if asJSON {
		return writeIndentedJSON(writer, paths)
	}
	for _, entry := range []struct{ label, value string }{
		{"Configuration directory", paths.ConfigDir}, {"Configuration file", paths.ConfigFile},
		{"Data directory", paths.DataDir}, {"State directory", paths.StateDir}, {"Cache directory", paths.CacheDir},
		{"Catalog runtime directory", paths.RuntimeDir}, {"Embedded baseline directory", paths.BaselineDir},
		{"Badger directory", paths.BadgerDir}, {"SQLite file", paths.SQLiteFile}, {"Uploaded files directory", paths.FilesDir},
		{"Local administrator token file", paths.LocalTokenFile}, {"Welcome stamp file", paths.WelcomeStampFile},
		{"Deployment", paths.DeploymentID}, {"Instance", paths.InstanceID},
	} {
		if _, err := fmt.Fprintf(writer, "%s: %s\n", entry.label, entry.value); err != nil {
			return err
		}
	}
	return nil
}

func writeValidation(writer io.Writer, asJSON bool, validationErr error) error {
	if asJSON {
		result := validationResult{Valid: validationErr == nil}
		if validationErr != nil {
			result.Error = validationErr.Error()
		}
		return writeIndentedJSON(writer, result)
	}
	_, err := fmt.Fprintln(writer, "Configuration is valid.")
	return err
}

func writeIndentedJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func flattenConfiguration(prefix string, value any, lines *[]string) {
	object, isObject := value.(map[string]any)
	if !isObject {
		encoded, err := json.Marshal(value)
		if err != nil {
			encoded = []byte(strconv.Quote(fmt.Sprint(value)))
		}
		*lines = append(*lines, prefix+"="+string(encoded))
		return
	}
	for key, child := range object {
		name := key
		if prefix != "" {
			name = prefix + "." + key
		}
		flattenConfiguration(name, child, lines)
	}
}
