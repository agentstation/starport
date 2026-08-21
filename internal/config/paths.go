package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const applicationDirectory = "starport"

const configDirectoryEnvironment = "STARPORT_CONFIG_DIR"

// Paths contains the platform-owned files and directories that Starport uses.
type Paths struct {
	ConfigDir      string `json:"config_dir"`
	ConfigFile     string `json:"config_file"`
	DataDir        string `json:"data_dir"`
	BadgerDir      string `json:"badger_dir"`
}

// PlatformPaths resolves the current user's Starport paths.
func PlatformPaths() (Paths, error) {
	if configured := os.Getenv(configDirectoryEnvironment); configured != "" {
		if !filepath.IsAbs(configured) {
			return Paths{}, fmt.Errorf("%s must be an absolute path", configDirectoryEnvironment)
		}
		return PathsForConfigDir(configured), nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user configuration directory: %w", err)
	}
	if root == "" {
		return Paths{}, fmt.Errorf("user configuration directory is empty")
	}
	return PathsForConfigDir(filepath.Join(root, applicationDirectory)), nil
}

// PathsForConfigDir derives all managed paths from one configuration directory.
func PathsForConfigDir(configDir string) Paths {
	configDir = filepath.Clean(configDir)
	dataDir := filepath.Join(configDir, "data")
	return Paths{
		ConfigDir:      configDir,
		ConfigFile:     filepath.Join(configDir, "config.env"),
		DataDir:        dataDir,
		BadgerDir:      filepath.Join(dataDir, "badger"),
	}
}
