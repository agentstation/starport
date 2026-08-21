package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformPathsUseUserConfigDirectory(t *testing.T) {
	t.Setenv(configDirectoryEnvironment, "")
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

func TestPlatformPathsUseExplicitDirectory(t *testing.T) {
	configured := t.TempDir()
	t.Setenv(configDirectoryEnvironment, configured)

	paths, err := PlatformPaths()
	if err != nil {
		t.Fatalf("resolve explicit config directory: %v", err)
	}
	if paths != PathsForConfigDir(configured) {
		t.Errorf("paths = %#v, want %#v", paths, PathsForConfigDir(configured))
	}
}

func TestPlatformPathsRejectRelativeExplicitDirectory(t *testing.T) {
	t.Setenv(configDirectoryEnvironment, "relative/config")
	if _, err := PlatformPaths(); err == nil {
		t.Fatal("relative config directory did not fail")
	}
}

func TestPathsForConfigDirDerivesManagedPaths(t *testing.T) {
	root := t.TempDir()
	paths := PathsForConfigDir(root)
	wants := map[string]string{
		"config": filepath.Join(root, "config.env"),
		"data":   filepath.Join(root, "data"),
		"badger": filepath.Join(root, "data", "badger"),
	}
	got := map[string]string{
		"config": paths.ConfigFile,
		"data":   paths.DataDir,
		"badger": paths.BadgerDir,
	}
	for name, want := range wants {
		if got[name] != want {
			t.Errorf("%s path = %q, want %q", name, got[name], want)
		}
	}
}
