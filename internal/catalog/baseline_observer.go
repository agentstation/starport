package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"

	starmaperrors "github.com/agentstation/starmap/pkg/errors"

	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// ErrNoFleetHead means that no gateway published a fleet head yet, so a baseline report has nothing to compare.
var ErrNoFleetHead = errors.New("no fleet head. A gateway publishes the first fleet head when it starts")

// observerDirectoryPattern names the temporary directory of a baseline observer.
const observerDirectoryPattern = "starport-baseline-"

// OpenBaselineObserver opens the fleet catalog runtime for one baseline report.
// It replays the shared fleet head into a new temporary directory and never takes
// the publication lease, so it runs beside a gateway on the same host. It never opens
// the state directory of the gateway. Close removes the temporary directory.
// A fleet without a head returns ErrNoFleetHead.
func OpenBaselineObserver(ctx context.Context, store storage.KVStore, db *sqlstore.DB, settings Settings, lookup DeploymentLookup) (*Runtime, error) {
	fleet, err := openRecoveryFleet(ctx, store, db, settings.DeploymentID)
	if err != nil {
		return nil, err
	}
	if fleet == nil {
		return nil, ErrBaselinePromotionFleetOnly
	}
	return observeFleet(ctx, fleet, settings, func(observing *FleetStore, observed Settings) (*Runtime, error) {
		return openRuntimeWithFleet(ctx, store, observing, observed, lookup)
	})
}

// observeFleet makes the fleet session write-refusing, then opens the runtime in a new
// temporary directory. The open function composes the runtime over the session.
func observeFleet(ctx context.Context, fleet *FleetStore, settings Settings, open func(*FleetStore, Settings) (*Runtime, error)) (*Runtime, error) {
	fleet.observeOnly()
	if _, err := fleet.CurrentHead(ctx); starmaperrors.IsNotFound(err) {
		return nil, ErrNoFleetHead
	} else if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", observerDirectoryPattern)
	if err != nil {
		return nil, err
	}
	observer, err := open(fleet, settings.observer(directory))
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

// observeOnly refuses the publication lease and every shared write of this session.
// A write that reaches the shared stores fails and increments refusedWrites.
func (s *FleetStore) observeOnly() {
	s.observe = true
	s.store = observingStore{IncarnationStore: s.store, refused: &s.refusedWrites}
	s.witness = observingWitness{fleetRecoveryWitness: s.witness, refused: &s.refusedWrites}
}

// errObserverWrite refuses a shared write of a baseline observer.
func errObserverWrite() error {
	return fleetStoreConflict("this process observes the fleet and writes no shared catalog state")
}

// observingStore reads the shared fleet keys and refuses every write.
type observingStore struct {
	storage.IncarnationStore
	refused *atomic.Int64
}

func (s observingStore) CompareAndSwap(context.Context, []storage.CompareAndSwapMutation, ...string) error {
	s.refused.Add(1)
	return errObserverWrite()
}

// observingWitness reads the recovery approval and refuses to consume the first-use grant.
type observingWitness struct {
	fleetRecoveryWitness
	refused *atomic.Int64
}

func (w observingWitness) ConsumeBootstrap(context.Context, recovery.Record) error {
	w.refused.Add(1)
	return errObserverWrite()
}
