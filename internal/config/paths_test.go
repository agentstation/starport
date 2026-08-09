package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformPathsUseUserConfigDirectory(t *testing.T) {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("resolve user config directory: %v", err)
	}
	paths, err := PlatformPaths()
	if err != nil {
		t.Fatalf("resolve platform paths: %v", err)
	}
	want := filepath.Join(userConfigDir, applicationDirectory)
	if paths.ConfigDir != want {
		t.Errorf("config directory = %q, want %q", paths.ConfigDir, want)
	}
}

func TestPathsForConfigDirDerivesManagedPaths(t *testing.T) {
	root := t.TempDir()
	paths := PathsForConfigDir(root)
	wants := map[string]string{
		"config": filepath.Join(root, "config.env"),
		"data":   filepath.Join(root, "data"),
		"badger": filepath.Join(root, "data", "badger"),
		"rates":  filepath.Join(root, "rate_limits.yaml"),
	}
	got := map[string]string{
		"config": paths.ConfigFile,
		"data":   paths.DataDir,
		"badger": paths.BadgerDir,
		"rates":  paths.RateLimitsFile,
	}
	for name, want := range wants {
		if got[name] != want {
			t.Errorf("%s path = %q, want %q", name, got[name], want)
		}
	}
}
