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
	layers     []catalogconfig.Layer
	values     map[string]string
}

// resolveCatalogLookuper applies product authorities before permitted Starmap fallbacks.
// Starmap validates source replacement and preserves explicit empty values.
func resolveCatalogLookuper(sources []envconfig.Lookuper) (envconfig.Lookuper, error) {
	layers := make([]catalogconfig.Layer, 0, len(sources)*2)
	for _, inherited := range []bool{false, true} {
		for index, source := range sources {
			layer, err := catalogLayer(source, index, inherited)
			if err != nil {
				return nil, err
			}
			layers = append(layers, layer)
		}
	}
	resolution, err := catalogconfig.Resolve(layers...)
	if err != nil {
		return nil, err
	}
	values, selected := resolvedCatalogValues(resolution, layers)
	lookupers := append([]envconfig.Lookuper{envconfig.MapLookuper(values)}, sources...)
	return catalogSettingsLookuper{
		Lookuper: envconfig.MultiLookuper(lookupers...), resolution: resolution, layers: layers, values: selected,
	}, nil
}

// catalogLayer reads the catalog values of one configuration source. The
// product layer reads Starport names. The inherited layer reads the Starmap
// names of deployment-scope settings.
func catalogLayer(source envconfig.Lookuper, index int, inherited bool) (catalogconfig.Layer, error) {
	values := make(map[string]string)
	for _, descriptor := range catalogconfig.Descriptors() {
		if descriptor.Name == catalogconfig.AuthorityOrigin {
			if !inherited {
				if _, present := source.Lookup(catalogEnvironmentName(descriptor.Name)); present {
					return catalogconfig.Layer{}, fmt.Errorf("%s is a Starmap server setting. Starport cannot issue catalog permissions", catalogEnvironmentName(descriptor.Name))
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
	return catalogconfig.Layer{Name: catalogLayerName(index, inherited), Values: values}, nil
}

func catalogLayerName(index int, inherited bool) string {
	if inherited {
		return "starmap-fallback-" + strconv.Itoa(index)
	}
	return "starport-source-" + strconv.Itoa(index)
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
