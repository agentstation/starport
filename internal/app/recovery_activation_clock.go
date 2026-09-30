package app

import (
	"context"
	goruntime "runtime"

	"github.com/agentstation/starmap/pkg/catalogs/permission"
	"github.com/agentstation/starmap/pkg/catalogs/permission/hostclock"
	"github.com/agentstation/starmap/pkg/catalogs/permission/hostclock/profile"
	"github.com/agentstation/starport/internal/config"
)

func inspectActivationClock(ctx context.Context, cfg *config.Config) (permission.ClockReading, error) {
	if cfg.Catalog.SourceStartupPolicy != config.CatalogStartupRequireAuthority {
		return permission.ClockReading{}, nil
	}
	settings := cfg.Catalog.PermissionClock
	if err := settings.Validate(); err != nil {
		return permission.ClockReading{}, err
	}
	if settings.Source != profile.Native {
		return permission.ClockReading{}, nil
	}
	observe := hostclock.Observe
	if goruntime.GOOS == "windows" {
		observer, err := hostclock.NewWindowsObserver(hostclock.WindowsProfile(settings.Windows))
		if err != nil {
			return permission.ClockReading{}, err
		}
		observe = observer.Observe
	}
	cache, err := permission.NewClockCache(permission.ClockCacheConfig{Observe: observe, Elapsed: hostclock.Elapsed, MaxAge: settings.MaxAge, MaxDriftPPM: settings.MaxDriftPPM, CounterUncertainty: settings.CounterUncertainty})
	if err != nil {
		return permission.ClockReading{}, err
	}
	if err := cache.Refresh(ctx); err != nil {
		return permission.ClockReading{}, err
	}
	return cache.Read(), nil
}
