package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenameNoReplaceRefusesEmptyDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "sentinel"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := renameNoReplace(source, destination); err == nil {
		t.Fatal("rename replaced an existing empty destination")
	}
	if _, err := os.Stat(filepath.Join(source, "sentinel")); err != nil {
		t.Fatalf("source after refused rename: %v", err)
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("destination entries = %d, want 0", len(entries))
	}
}
