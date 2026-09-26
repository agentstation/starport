package config

import (
	"fmt"
	"maps"
	"strconv"
	"strings"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/productpaths"
	"github.com/sethvargo/go-envconfig"
)

type catalogSettingsLookuper struct {
	pathLayers []productpaths.Layer
	fileInputs []configurationFile
	sources    []envconfig.Lookuper
	envconfig.Lookuper
	resolution catalogconfig.Resolution
	values     map[string]string
}

// resolveCatalogLookuper applies product authorities before permitted Starmap fallbacks.
// Starmap validates source replacement and preserves explicit empty values.
func resolveCatalogLookuper(sources []envconfig.Lookuper) (envconfig.Lookuper, error) {
	layers := make([]catalogconfig.Layer, 0, len(sources)*2)
	for _, inherited := range []bool{false, true} {
		for index, source := range sources {
			values := make(map[string]string)
			for _, descriptor := range catalogconfig.Descriptors() {
				if descriptor.Name == catalogconfig.AuthorityOrigin {
					if !inherited {
						if _, present := source.Lookup(catalogEnvironmentName(descriptor.Name)); present {
							return nil, fmt.Errorf("%s is a Starmap server setting. Starport cannot issue catalog permissions", catalogEnvironmentName(descriptor.Name))
						}
					}
					continue
				}
				if strings.HasPrefix(descriptor.Name, starmapClockPrefix) {
					continue
				}
				name := catalogEnvironmentName(descriptor.Name)
				if inherited {
					if descriptor.Scope != catalogconfig.DeploymentScope {
						continue
					}
					name = descriptor.Name
				}
				if value, present := source.Lookup(name); present {
					values[descriptor.Name] = value
				}
			}
			origin := "starport-source-" + strconv.Itoa(index)
			if inherited {
				origin = "starmap-fallback-" + strconv.Itoa(index)
			}
			layers = append(layers, catalogconfig.Layer{Name: origin, Values: values})
		}
	}
	resolution, err := catalogconfig.Resolve(layers...)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string)
	selected := make(map[string]string)
	for _, descriptor := range catalogconfig.Descriptors() {
		if strings.HasPrefix(descriptor.Name, starmapClockPrefix) {
			continue
		}
		value := descriptor.Default
		if origin, present := resolution.Origins[descriptor.Name]; present {
			for _, layer := range layers {
				if layer.Name == origin {
					value = layer.Values[descriptor.Name]
					selected[descriptor.Name] = value
					break
				}
			}
		}
		values[catalogEnvironmentName(descriptor.Name)] = value
	}
	lookupers := append([]envconfig.Lookuper{envconfig.MapLookuper(values)}, sources...)
	return catalogSettingsLookuper{
		Lookuper: envconfig.MultiLookuper(lookupers...), resolution: resolution, values: selected,
	}, nil
}

func catalogEnvironmentName(name string) string {
	if name == catalogconfig.StateDirectory {
		return stateDirectoryEnvironment
	}
	return "STARPORT_" + strings.TrimPrefix(name, catalogconfig.Prefix)
}

// CatalogValues returns caller-owned settings for the canonical runtime parser.
// Values can contain source credentials and must not enter diagnostics.
func (c CatalogConfig) CatalogValues() map[string]string {
	return maps.Clone(c.canonicalValues)
}
