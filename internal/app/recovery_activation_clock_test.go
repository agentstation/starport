package app

import (
	"github.com/agentstation/starmap/pkg/catalogs/permission"
	"github.com/agentstation/starmap/pkg/catalogs/permission/hostclock/profile"
	"github.com/agentstation/starport/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRecoveryStandalonePermissionNeedsNoQualifiedClock(t *testing.T) {
	cfg, _, _, _ := operatorInputFixture(t)
	// An unusable custom clock must not disable permission that requires no absolute authority deadline.
	cfg.Catalog.PermissionClock.Source = profile.Native
	cfg.Catalog.PermissionClock.MaxAge = 0
	reading, err := inspectActivationClock(t.Context(), cfg)
	require.NoError(t, err)
	require.Equal(t, permission.ClockReading{}, reading)
	cfg.Catalog.SourceStartupPolicy = config.CatalogStartupRequireAuthority
	_, err = inspectActivationClock(t.Context(), cfg)
	require.Error(t, err, "internal authority retains its qualified-clock requirements")
}
