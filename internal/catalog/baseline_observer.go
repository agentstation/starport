package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// OpenBaselineObserver opens the fleet catalog runtime for one baseline report.
// It replays the shared fleet head into a new temporary directory and never takes
// the publication lease, so it runs beside a gateway on the same host. It never opens
// the state directory of the gateway. Close removes the temporary directory.
func OpenBaselineObserver(ctx context.Context, store storage.KVStore, db *sqlstore.DB, settings Settings, lookup DeploymentLookup) (*Runtime, error) {
	fleet, err := openRecoveryFleet(ctx, store, db, settings.DeploymentID)
	if err != nil {
		return nil, err
	}
	if fleet == nil {
		return nil, ErrBaselinePromotionFleetOnly
	}
	fleet.observe = true
	directory, err := os.MkdirTemp("", "starport-baseline-")
	if err != nil {
		return nil, err
	}
	observer, err := openRuntimeWithFleet(ctx, store, fleet, settings.observer(directory), lookup)
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(directory))
	}
	observer.temporary = directory
	return observer, nil
}

// observer moves every directory that the runtime writes into one temporary directory.
// An empty credential policy directory selects the ephemeral resolver.
func (s Settings) observer(directory string) Settings {
	s.StateDirectory = filepath.Join(directory, "state")
	s.SourceCacheDirectory = filepath.Join(directory, "cache")
	s.BaselineDirectory = filepath.Join(directory, "baseline")
	s.CredentialPolicyDirectory = ""
	return s
}
