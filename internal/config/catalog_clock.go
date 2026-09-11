package config

import (
	"strings"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/catalogs/permission/hostclock/profile"
	"github.com/sethvargo/go-envconfig"
)

const (
	starportClockPrefix = "STARPORT_CATALOG_PERMISSION_CLOCK_"
	starmapClockPrefix  = "STARMAP_CATALOG_PERMISSION_CLOCK_"
)

// catalogClockLookuper applies clock aliases within one configuration source.
// Source precedence stays unchanged. The Starport name wins within the same source.
type catalogClockLookuper struct {
	envconfig.Lookuper
}

func (l catalogClockLookuper) Lookup(name string) (string, bool) {
	if value, present := l.Lookuper.Lookup(name); present {
		return value, true
	}
	if suffix, clock := strings.CutPrefix(name, starportClockPrefix); clock {
		return l.Lookuper.Lookup(starmapClockPrefix + suffix)
	}
	return "", false
}

// loadPermissionClock uses Starmap's schema and parser without a native clock dependency.
func loadPermissionClock(lookuper envconfig.Lookuper) (profile.Config, error) {
	values := make(map[string]string)
	for _, descriptor := range catalogconfig.Descriptors() {
		suffix, clock := strings.CutPrefix(descriptor.Name, starmapClockPrefix)
		if !clock {
			continue
		}
		if value, present := lookuper.Lookup(starportClockPrefix + suffix); present {
			values[descriptor.Name] = value
		}
	}
	parsed, err := catalogconfig.Parse(values)
	if err != nil {
		return profile.Config{}, err
	}
	return parsed.PermissionClock, nil
}
