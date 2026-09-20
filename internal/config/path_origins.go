package config

import (
	"strconv"
	"strings"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/productpaths"
)

type pathSelection struct {
	name, environment, canonical string
	root                         productpaths.Root
	value                        *string
	required                     bool
}

func pathSelections(cfg *Config) []pathSelection {
	selections := []pathSelection{
		{"workspace", "", catalogconfig.WorkspacePath, "", &cfg.Catalog.WorkspacePath, false},
		{pathRoleRuntime, "", catalogconfig.StateDirectory, productpaths.State, &cfg.Catalog.StateDirectory, !cfg.Catalog.StateDirectoryIsScratch()},
		{"local-token", "", "", productpaths.Data, &cfg.Security.LocalTokenPath, true},
		{"tls-certificate", "STARPORT_SECURITY_TLS_CERT_PATH", "", "", &cfg.Security.TLSCertPath, false},
		{cacheCAFileRole, cacheCAFileEnvironment, "", "", &cfg.Cache.CAFile, false},
		{"tls-key", "STARPORT_SECURITY_TLS_KEY_PATH", "", "", &cfg.Security.TLSKeyPath, false},
		{"logs", "STARPORT_LOGGING_FILE_PATH", "", "", &cfg.Logging.FilePath, false},
	}
	if cfg.Catalog.Source == CatalogSourceFile {
		selections = append(selections, pathSelection{"source-file", "", catalogconfig.SourceURL, "", &cfg.Catalog.SourceURL, true})
	}
	if cfg.Storage.Mode == storageModeBadger && !cfg.Storage.Badger.inMemory {
		selections = append(selections, pathSelection{pathRoleBadger, badgerPathEnvironment, "", productpaths.Data, &cfg.Storage.Badger.Path, true})
	}
	if cfg.Storage.SQL.Mode == sqlModeSQLite && !cfg.Storage.Badger.inMemory {
		selections = append(selections, pathSelection{pathRoleSQLite, sqlitePathEnvironment, "", productpaths.Data, &cfg.Storage.SQL.SQLite.Path, true})
	}
	if cfg.Files.SelectedBackend() == BlobBackendFilesystem && !cfg.Catalog.StateDirectoryIsScratch() {
		selections = append(selections, pathSelection{pathRoleFiles, filesPathEnvironment, "", productpaths.Data, &cfg.Files.Path, true})
	}
	return selections
}

type pathSelectionOrigin struct {
	value, origin string
}

// loadedPathOrigins records selection before caller overrides and path anchoring.
func loadedPathOrigins(cfg *Config, selected catalogSettingsLookuper) map[string]pathSelectionOrigin {
	origins := make(map[string]pathSelectionOrigin)
	for _, selection := range pathSelections(cfg) {
		origin := pathOriginDefault
		if selection.root != "" {
			origin = "derived:" + string(selection.root)
		}
		if selection.canonical != "" {
			if resolved, ok := selected.resolution.Origins[selection.canonical]; ok {
				origin = catalogPathOrigin(resolved, selected)
			}
		} else if selection.environment != "" {
			for index, source := range selected.sources {
				if _, present := source.Lookup(selection.environment); present {
					origin = selected.pathLayers[index].Name
					break
				}
			}
		}
		origins[selection.name] = pathSelectionOrigin{value: *selection.value, origin: origin}
	}
	return origins
}

func catalogPathOrigin(resolved string, selected catalogSettingsLookuper) string {
	for _, prefix := range []string{"starport-source-", "starmap-fallback-"} {
		if suffix, ok := strings.CutPrefix(resolved, prefix); ok {
			index, err := strconv.Atoi(suffix)
			if err != nil || index < 0 || index >= len(selected.pathLayers) {
				return resolved
			}
			origin := selected.pathLayers[index].Name
			if prefix == "starmap-fallback-" {
				origin = "starmap-fallback:" + origin
			}
			return origin
		}
	}
	return resolved
}
