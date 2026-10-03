package config

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productpaths"
)

// referencePlatform names one release platform and the native notation of
// its default user-directory roots. productpaths.UserDefaults selects these
// roots from runtime.GOOS. TestReferencePlatformMatchesUserDefaults proves
// the notation for the host platform.
type referencePlatform struct {
	ID        string
	Title     string
	Separator string
	Roots     map[productpaths.Root]string
}

// referencePlatforms lists the platforms that the release builds.
var referencePlatforms = []referencePlatform{
	{ID: "linux", Title: "Linux", Separator: "/", Roots: map[productpaths.Root]string{
		productpaths.Config: "~/.config/starport", productpaths.Data: "~/.local/share/starport",
		productpaths.State: "~/.local/state/starport", productpaths.Cache: "~/.cache/starport",
	}},
	{ID: "darwin", Title: "macOS", Separator: "/", Roots: map[productpaths.Root]string{
		productpaths.Config: "~/Library/Application Support/starport/config", productpaths.Data: "~/Library/Application Support/starport/data",
		productpaths.State: "~/Library/Application Support/starport/state", productpaths.Cache: "~/Library/Caches/starport",
	}},
	{ID: "windows", Title: "Windows", Separator: `\`, Roots: map[productpaths.Root]string{
		productpaths.Config: `%AppData%\starport`, productpaths.Data: `%LocalAppData%\starport\data`,
		productpaths.State: `%LocalAppData%\starport\state`, productpaths.Cache: `%LocalAppData%\starport\cache`,
	}},
}

var referenceRoots = []productpaths.Root{productpaths.Config, productpaths.Data, productpaths.State, productpaths.Cache}

// defaultFileManifest loads the documented default configuration with
// placeholder platform roots and an empty environment. The loader selects
// the primary configuration file below the placeholder configuration root.
// No such file exists, so the loader reads no configuration value.
func defaultFileManifest(ctx context.Context) (productpaths.FileManifest, error) {
	loader := NewLoader().WithEnvironment(nil)
	loader.platformDefaults = func(root productpaths.Root) (string, error) { return placeholderRoot(root), nil }
	cfg, err := loader.Load(ctx)
	if err != nil {
		return productpaths.FileManifest{}, err
	}
	return cfg.FileManifest("")
}

// placeholderRoot returns an absolute host path that stands for one root.
func placeholderRoot(root productpaths.Root) string {
	path := filepath.FromSlash("/starport-reference-" + string(root))
	if !filepath.IsAbs(path) {
		path = "C:" + path
	}
	return path
}

// manifest returns a copy of the placeholder manifest with each root in the
// platform notation.
func (platform referencePlatform) manifest(placeholder productpaths.FileManifest) productpaths.FileManifest {
	report := placeholder
	report.Roots = make(productpaths.Roots, len(placeholder.Roots))
	for root, path := range placeholder.Roots {
		report.Roots[root] = platform.path(path)
	}
	report.Files = make([]productpaths.FileEntry, len(placeholder.Files))
	for index, entry := range placeholder.Files {
		entry.Location = platform.path(entry.Location)
		report.Files[index] = entry
	}
	return report
}

func (platform referencePlatform) path(path productpaths.Path) productpaths.Path {
	path.Path, path.Anchor = platform.location(path.Path), platform.location(path.Anchor)
	return path
}

func (platform referencePlatform) location(path string) string {
	for _, root := range referenceRoots {
		rest, ok := strings.CutPrefix(path, placeholderRoot(root))
		if !ok || (rest != "" && rest[0] != filepath.Separator) {
			continue
		}
		return platform.Roots[root] + strings.ReplaceAll(filepath.ToSlash(rest), "/", platform.Separator)
	}
	return path
}
