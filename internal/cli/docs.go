package cli

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	urfavecli "github.com/urfave/cli/v3"
)

// DocsSource returns the documentation site that the build embeds. It
// returns false when the build has no documentation site.
type DocsSource func() (fs.FS, bool)

const (
	flagDocsForce = "force"
	// docsManifest is the docs build manifest at the root of the site.
	docsManifest = "manifest.json"
	// docsReleaseUnknown is the release that the export reports when the
	// site has no readable manifest.
	docsReleaseUnknown = "unknown"
)

const docsNotBuilt = "this build has no documentation site; " +
	"run `pnpm -C console build` before `go build`, or use a release binary"

// docsManifestRelease is the part of the docs manifest that the export
// reports.
type docsManifestRelease struct {
	Release string `json:"starport_release"`
}

func newDocsCommand(deps Dependencies, usageError usageErrorHandler) *urfavecli.Command {
	export := &urfavecli.Command{
		Name:         "export",
		Usage:        "Write the embedded documentation site to a directory",
		ArgsUsage:    "<directory>",
		OnUsageError: usageError,
		Flags: []urfavecli.Flag{
			&urfavecli.BoolFlag{Name: flagDocsForce, Usage: "Write into a non-empty directory and replace files that have the same path"},
		},
		Action: func(_ context.Context, cmd *urfavecli.Command) error {
			if cmd.NArg() != 1 || cmd.Args().First() == "" {
				return urfavecli.Exit(cmd.FullName()+" requires one directory argument", ExitCodeUsage)
			}
			var docs fs.FS
			built := false
			if deps.Docs != nil {
				docs, built = deps.Docs()
			}
			if !built {
				return runtimeFailure{cause: errors.New(docsNotBuilt)}
			}
			directory := filepath.Clean(cmd.Args().First())
			count, err := exportDocs(docs, directory, cmd.Bool(flagDocsForce))
			if err != nil {
				return runtimeFailure{cause: err}
			}
			_, err = fmt.Fprintf(cmd.Writer, "Exported %d files to %s\nRelease: %s\n", count, directory, docsRelease(docs))
			return err
		},
	}
	return &urfavecli.Command{
		Name: "docs", Usage: "Work with the documentation site that the build embeds",
		OnUsageError: usageError, Commands: []*urfavecli.Command{export},
	}
}

// exportDocs copies the site into the directory and returns the file count.
// The directory must be absent or empty unless force is set. An os.Root
// keeps each write inside the directory.
func exportDocs(docs fs.FS, directory string, force bool) (int, error) {
	// #nosec G301 -- the export is public documentation for a static host,
	// so other users must read it.
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return 0, fmt.Errorf("create export directory: %w", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0, fmt.Errorf("read export directory: %w", err)
	}
	if len(entries) > 0 && !force {
		return 0, fmt.Errorf("export directory %s is not empty; use --%s to write into it", directory, flagDocsForce)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return 0, fmt.Errorf("open export directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	count := 0
	err = fs.WalkDir(docs, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == "." {
			return err
		}
		name := filepath.FromSlash(path)
		if entry.IsDir() {
			return root.MkdirAll(name, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		data, err := fs.ReadFile(docs, path)
		if err != nil {
			return err
		}
		if err := root.WriteFile(name, data, 0o644); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return count, fmt.Errorf("export documentation site: %w", err)
	}
	return count, nil
}

// docsRelease returns the release that the site manifest records.
func docsRelease(docs fs.FS) string {
	data, err := fs.ReadFile(docs, docsManifest)
	if err != nil {
		return docsReleaseUnknown
	}
	var manifest docsManifestRelease
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Release == "" {
		return docsReleaseUnknown
	}
	return manifest.Release
}
