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
	ConfigDir  string `json:"config_dir"`
	ConfigFile string `json:"config_file"`
	DataDir    string `json:"data_dir"`
	BadgerDir  string `json:"badger_dir"`
	// SQLiteFile holds the embedded relational database. It sits in the data
	// directory beside the Badger store: the same machine state, the other
	// shape.
	SQLiteFile string `json:"sqlite_file"`
	// FilesDir roots the filesystem blob backend. It sits in the data
	// directory beside the record store, because the bytes are state this
	// machine holds rather than a decision an operator wrote down.
	FilesDir string `json:"files_dir"`
	// LocalTokenFile holds this machine's local admin token. It sits in the
	// data directory rather than beside the configuration file, because it is
	// state this machine generated and not a decision an operator wrote down.
	LocalTokenFile string `json:"local_token_file"`
	// WelcomeStampFile records that this machine has been told how to open the
	// console. It exists so the greeting prints once: an operator who has run
	// the gateway before is not a new operator, and a banner that repeats every
	// start is one an experienced reader learns to skip past the day they
	// needed it.
	WelcomeStampFile string `json:"welcome_stamp_file"`
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
		ConfigDir:        configDir,
		ConfigFile:       filepath.Join(configDir, "config.env"),
		DataDir:          dataDir,
		BadgerDir:        filepath.Join(dataDir, "badger"),
		SQLiteFile:       filepath.Join(dataDir, "sqlite", "starport.db"),
		FilesDir:         filepath.Join(dataDir, "files"),
		LocalTokenFile:   filepath.Join(dataDir, "local-admin-token.json"),
		WelcomeStampFile: filepath.Join(dataDir, "welcomed"),
	}
}
