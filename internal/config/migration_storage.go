package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// MigrationStorageSelection identifies the durable backend without its credentials.
func (c StorageConfig) MigrationStorageSelection() (string, error) {
	var selection string
	switch c.Mode {
	case storageModeBadger:
		if c.Badger.inMemory || !filepath.IsAbs(c.Badger.Path) {
			return "", fmt.Errorf("migration requires an absolute persistent Badger path")
		}
		selection = "badger\x00" + filepath.Clean(c.Badger.Path)
	case storageModeValkey:
		endpoint, err := url.Parse(c.Valkey.URL)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "valkey" && endpoint.Scheme != "redis") {
			return "", fmt.Errorf("migration requires a valid Valkey endpoint")
		}
		// The adapter selects the address and database independently of credentials.
		selection = "valkey\x00" + strings.ToLower(endpoint.Host) + "\x00" + endpoint.EscapedPath() + "\x00" + strconv.FormatBool(c.Valkey.ClusterMode)
	default:
		return "", fmt.Errorf("migration requires a supported persistent KV backend")
	}
	digest := sha256.Sum256([]byte(selection))
	return hex.EncodeToString(digest[:]), nil
}

func (c *Config) verifySavedMigrationStorage(primary map[string]string) error {
	saved := StorageConfig{Mode: primary["STARPORT_STORAGE_MODE"]}
	if saved.Mode == "" {
		saved.Mode = storageModeBadger
	}
	switch saved.Mode {
	case storageModeBadger:
		saved.Badger.Path = primary[badgerPathEnvironment]
	case storageModeValkey:
		saved.Valkey.URL = primary["STARPORT_STORAGE_VALKEY_URL"]
		if raw := primary["STARPORT_STORAGE_VALKEY_CLUSTER_MODE"]; raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				return fmt.Errorf("save a valid KV cluster selection in primary configuration")
			}
			saved.Valkey.ClusterMode = value
		}
	}
	expected, err := c.Storage.MigrationStorageSelection()
	if err != nil {
		return err
	}
	retained, err := saved.MigrationStorageSelection()
	if err != nil || retained != expected {
		return fmt.Errorf("primary configuration must save the selected KV backend and absolute Badger path or Valkey endpoint")
	}
	return nil
}
