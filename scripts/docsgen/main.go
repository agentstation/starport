package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentstation/starport/internal/config"
)

func main() {
	files, err := config.ReferenceFiles(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "reference generation failed: %v\n", err)
		os.Exit(1)
	}
	directory := filepath.FromSlash(config.ReferenceDirectory)
	// #nosec G301 -- the pages are committed documentation source, so other
	// users must read the directory.
	if err := os.MkdirAll(directory, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "reference generation failed: %v\n", err)
		os.Exit(1)
	}
	for _, file := range files {
		// #nosec G306 -- the pages are committed documentation source, so
		// other users must read them.
		if err := os.WriteFile(filepath.Join(directory, file.Name), file.Data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "reference generation failed: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Printf("PASS wrote %d reference files to %s\n", len(files), config.ReferenceDirectory)
}
