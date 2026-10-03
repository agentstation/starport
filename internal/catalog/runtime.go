package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/acquisition"
	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/remote"
	"github.com/agentstation/starmap/runtime"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// updateBuffer is the depth of the candidate channel. It absorbs a burst of
// candidates while acceptance works, so the connected runtime rarely waits.
// A full buffer holds the offer until acceptance reads or the runtime closes,
// because every candidate must reach route validation exactly once.
const updateBuffer = 8

// Runtime owns one Starmap connected runtime, Starport's accepted catalog
// head, and the routable control plane derived from that head.
//
// One runtime reads one source. The local-or-remote choice is gone: a
// deployment names a source kind, and the same composition serves every kind.
// Starmap publishes an effective generation into the candidate store.
// Starport validates the candidate before it advances the accepted head.
// A candidate that fails route validation never routes a request.
type Runtime struct {
	runtime *runtime.Runtime
	fleet   *FleetStore

	// cascade is the upstream Starmap source this deployment reads, when it
	// reads another Starmap runtime. The connected runtime does not own an
	// injected source, so this runtime closes it.
	cascade *remote.Source

	candidates *GenerationStore
	accepted   *GenerationStore
	control    *ControlPlane
	leases     *LeaseStore
	updates    chan Candidate

	// validation holds what happened to the newest candidate this instance
	// observed. The accepted head is durable. This record says how the
	// instance reached it.
	validation validationRecord

	mu       sync.Mutex
	started  bool
	cancel   context.CancelFunc
	watch    chan struct{}
	lastSeen catalogStateIdentity
}

// catalogStateIdentity is the pair that decides whether a candidate is new.
type catalogStateIdentity struct {
	generationID    string
	payloadChecksum string
	fleetRevision   uint64
}

type runtimeCollectors struct {
	providers runtime.Acquirer
	metadata  runtime.SourceAcquirer
	fleet     *FleetStore
}

// OpenRuntime composes one connected Starmap runtime over Starport's durable
// storage. It starts the source and acquisition schedules and returns.
//
// The deployment lookup is the only credential plane catalog acquisition
// reads, so an inference credential can reach no provider observation.
func OpenRuntime(
	ctx context.Context,
	store storage.KVStore,
	settings Settings,
	lookup DeploymentLookup,
) (*Runtime, error) {
	return openRuntimeWithRecovery(ctx, store, nil, settings, lookup)
}

// OpenRuntimeWithRecovery uses an independent SQL witness for a shared KV deployment.
// An unknown or closed witness cannot approve a backend during startup.
func OpenRuntimeWithRecovery(ctx context.Context, store storage.KVStore, db *sqlstore.DB, settings Settings, lookup DeploymentLookup) (*Runtime, error) {
	return openRuntimeWithRecovery(ctx, store, db, settings, lookup)
}

func openRuntimeWithRecovery(ctx context.Context, store storage.KVStore, db *sqlstore.DB, settings Settings, lookup DeploymentLookup) (*Runtime, error) {
	fleet, err := openRecoveryFleet(ctx, store, db, settings.DeploymentID)
	if err != nil {
		return nil, err
	}
	fleet.fencePolicy(settings.AppliedPolicyChecksum)
	if _, err := settings.starmapOptions(); err != nil {
		return nil, fmt.Errorf("configure Starmap runtime: %w", err)
	}
	if err := settings.ValidateStorageSelection(ctx); err != nil {
		return nil, err
	}
	state, err := settings.credentialPolicy(ctx, store)
	if err != nil {
		return nil, err
	}
	resolver := newAcquisitionResolver(ctx, lookup, state)
	if resolver.err != nil {
		return nil, fmt.Errorf("open catalog credential policy: %w", resolver.err)
	}
	providers, err := acquisition.NewAcquirer(
		acquisition.WithAcquirerCredentialResolver(resolver),
	)
	if err != nil {
		return nil, fmt.Errorf("open Starmap acquisition: %w", err)
	}
	metadata, err := settings.metadataCollector()
	if err != nil {
		return nil, fmt.Errorf("open Starmap metadata acquisition: %w", err)
	}
	return openRuntime(ctx, store, settings, runtimeCollectors{providers: providers, metadata: metadata, fleet: fleet})
}

// openRuntime composes the connected runtime with host-supplied provider and metadata collectors.
func openRuntime(
	ctx context.Context,
	store storage.KVStore,
	settings Settings,
	collectors runtimeCollectors,
) (*Runtime, error) {
	return openRuntimeWithMigration(ctx, store, settings, collectors, nil)
}

func openRuntimeWithMigration(
	ctx context.Context,
	store storage.KVStore,
	settings Settings,
	collectors runtimeCollectors,
	migration *runtime.DirectoryMigrationRequest,
) (*Runtime, error) {
	if ctx == nil {
		return nil, errors.New("catalog runtime context is required")
	}
	options, err := settings.starmapOptions()
	if err != nil {
		return nil, fmt.Errorf("configure Starmap runtime: %w", err)
	}
	if migration != nil {
		options = append(options, runtime.WithPublishedDirectoryMigration(*migration))
	}
	if err := settings.ValidateStorageSelection(ctx); err != nil {
		return nil, err
	}
	if settings.BaselineDirectory != "" {
		if _, err := starmap.ExportEmbeddedBaseline(ctx, settings.BaselineDirectory); err != nil {
			return nil, fmt.Errorf("persist embedded catalog baseline: %w", err)
		}
	}
	acceptedStore, err := NewGenerationStore(store)
	if err != nil {
		return nil, err
	}
	candidateStore, err := newCandidateGenerationStore(store)
	if err != nil {
		return nil, err
	}
	leases, err := NewLeaseStore(store)
	if err != nil {
		return nil, err
	}
	if collectors.fleet != nil {
		acceptedStore.fleet, acceptedStore.fleetAccepted = collectors.fleet, true
		candidateStore.fleet = collectors.fleet
	}
	acceptedClient, err := starmap.NewContext(ctx, starmap.WithCatalogStore(acceptedStore))
	if err != nil {
		return nil, fmt.Errorf("open accepted Starmap catalog: %w", err)
	}

	if collectors.fleet != nil {
		options = append(options, runtime.WithFleetStore(collectors.fleet))
	} else {
		options = append(options, runtime.WithClientOptions(starmap.WithCatalogStore(candidateStore)), runtime.WithLeaseStore(leases))
	}
	var cascade *remote.Source
	if settings.Source == string(runtime.SourceStarmap) {
		cascade, err = settings.cascadeSource(ctx)
		if err != nil {
			return nil, err
		}
		options = append(options, runtime.WithSource(cascade))
	}
	if collectors.providers != nil {
		options = append(options, runtime.WithAcquirer(collectors.providers))
	}
	if collectors.metadata != nil {
		options = append(options, runtime.WithSourceAcquirer(collectors.metadata))
	}
	connected, err := runtime.Open(ctx, options...)
	if err != nil {
		if cascade != nil {
			_ = cascade.Close()
		}
		return nil, fmt.Errorf("open Starmap runtime: %w", err)
	}
	control, err := Open(acceptedCatalogSource{Source: acceptedClient, catalogAttemptPermission: connected})
	if err != nil {
		_ = connected.Close()
		if cascade != nil {
			_ = cascade.Close()
		}
		return nil, err
	}
	result := newRuntime(connected, cascade, candidateStore, acceptedStore, control, leases)
	result.fleet = collectors.fleet
	return result, nil
}

func newRuntime(
	connected *runtime.Runtime,
	cascade *remote.Source,
	candidates, accepted *GenerationStore,
	control *ControlPlane,
	leases *LeaseStore,
) *Runtime {
	return &Runtime{
		runtime:    connected,
		cascade:    cascade,
		candidates: candidates,
		accepted:   accepted,
		control:    control,
		leases:     leases,
		updates:    make(chan Candidate, updateBuffer),
	}
}

// ControlPlane returns Starport's generation-consistent catalog projection. It
// reads the accepted head alone.
func (r *Runtime) ControlPlane() *ControlPlane {
	if r == nil {
		return nil
	}
	return r.control
}

// Refresh reads the source and observes every eligible provider. Overlapping
// callers join one run, because the connected runtime keeps refresh
// single-flight and returns the report of the run in flight.
func (r *Runtime) Refresh(ctx context.Context) (runtime.RefreshReport, error) {
	if r == nil || r.runtime == nil {
		return runtime.RefreshReport{}, ErrCatalogSourceRequired
	}
	if ctx == nil {
		return runtime.RefreshReport{}, errors.New("catalog refresh context is required")
	}
	return r.runtime.Refresh(ctx)
}

// RefreshCandidate refreshes the connected runtime and returns the effective
// state the refresh produced, with the lease epoch that fences its acceptance.
//
// A timeout of zero adds no cap. The transfer bounds already end a transfer
// that stops making progress, so an added cap would cut a transfer the
// transfer policy still allows.
func (r *Runtime) RefreshCandidate(
	ctx context.Context,
	timeout time.Duration,
) (Candidate, error) {
	if r == nil || r.runtime == nil {
		return Candidate{}, ErrCatalogSourceRequired
	}
	if ctx == nil {
		return Candidate{}, errors.New("catalog refresh context is required")
	}
	refreshCtx, cancel := refreshContext(ctx, timeout)
	defer cancel()
	if _, err := r.runtime.Refresh(refreshCtx); err != nil {
		return Candidate{}, err
	}
	return r.candidate(ctx)
}

// refreshContext bounds one refresh run. A positive timeout caps the run. A
// timeout of zero adds no cap, and the returned cancel still ends the run when
// the caller returns.
func refreshContext(
	ctx context.Context,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}

// CurrentCandidate returns the effective state the connected runtime serves
// now, with the lease epoch that fences its acceptance.
func (r *Runtime) CurrentCandidate(ctx context.Context) (Candidate, error) {
	if r == nil || r.runtime == nil {
		return Candidate{}, ErrCatalogSourceRequired
	}
	return r.candidate(ctx)
}

func (r *Runtime) candidate(ctx context.Context) (Candidate, error) {
	state := r.runtime.State()
	if state.Catalog == nil || state.GenerationID == "" {
		return Candidate{}, ErrCatalogRequired
	}
	candidate, err := r.candidateFromState(ctx, state)
	if err != nil {
		return Candidate{}, err
	}
	r.validation.observe(candidate)
	return candidate, nil
}

// RouteValidation reports where the newest candidate stands between the source
// and the routable head.
func (r *Runtime) RouteValidation() RouteValidation {
	if r == nil {
		return RouteValidation{State: RouteValidationUnknown}
	}
	return r.validation.snapshot()
}

// Reject records one candidate this instance refused, with the safe cause of
// the refusal. The accepted head does not move.
func (r *Runtime) Reject(candidate Candidate, failure error) {
	if r == nil {
		return
	}
	r.validation.reject(candidate, failure)
}

// Status returns the connected runtime status. It carries both provenances:
// the upstream generation the source published and the effective generation
// this runtime built from it.
func (r *Runtime) Status() runtime.Status {
	if r == nil || r.runtime == nil {
		return runtime.Status{}
	}
	return r.runtime.Status()
}

// PolicyState reports the last comparison of this replica's applied
// configuration with the applied fleet policy.
func (r *Runtime) PolicyState() string {
	if r == nil {
		return ""
	}
	return r.fleet.PolicyState()
}

// AcceptedGeneration returns the head Starport accepted. A deployment that
// accepted nothing yet reports the not-found error.
func (r *Runtime) AcceptedGeneration(ctx context.Context) (catalogs.Generation, error) {
	if r == nil || r.accepted == nil {
		return catalogs.Generation{}, ErrCatalogSourceRequired
	}
	return r.accepted.Current(ctx)
}

// Start forwards every new candidate the connected runtime publishes.
func (r *Runtime) Start(ctx context.Context) error {
	if r == nil || r.runtime == nil {
		return ErrCatalogSourceRequired
	}
	if ctx == nil {
		return errors.New("catalog runtime context is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return nil
	}
	watchCtx, cancel := context.WithCancel(ctx)
	watch := make(chan struct{})
	r.started = true
	r.cancel = cancel
	r.watch = watch
	go func() {
		defer close(watch)
		var work sync.WaitGroup
		work.Go(func() { r.forwardFrom(watchCtx, r.runtime.Updates()) })
		if r.fleet != nil {
			work.Go(func() { r.executePromotions(watchCtx) })
		}
		work.Wait()
	}()
	return nil
}

// Updates emits distinct candidates and retries shared candidates after a failed validation.
func (r *Runtime) Updates() <-chan Candidate {
	if r == nil {
		return nil
	}
	return r.updates
}

// Close stops the forwarding work and closes the connected runtime.
func (r *Runtime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	cancel := r.cancel
	watch := r.watch
	r.cancel = nil
	r.watch = nil
	r.started = false
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if watch != nil {
		select {
		case <-watch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if r.runtime == nil {
		return nil
	}
	err := r.runtime.Close()
	if r.cascade != nil {
		if cascadeErr := r.cascade.Close(); err == nil {
			err = cascadeErr
		}
	}
	return err
}

func (r *Runtime) forwardFrom(ctx context.Context, updates <-chan starmap.CatalogState) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	var pending *starmap.CatalogState
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if r.fleet != nil {
				r.offer(ctx, r.runtime.State())
			} else if pending != nil && r.offer(ctx, *pending) {
				pending = nil
			}
		case state, open := <-updates:
			if !open {
				return
			}
			if r.offer(ctx, state) {
				pending = nil
			} else {
				pending = &state
			}
		}
	}
}

func (r *Runtime) offer(ctx context.Context, state starmap.CatalogState) bool {
	if r.fleet != nil {
		// A queued notification can precede the current durable publication.
		state = r.runtime.State()
		status, ok := r.runtime.FleetStatus()
		if ok && status.Head.GenerationID == state.GenerationID {
			identity := stateIdentity(state)
			identity.fleetRevision = status.Head.Revision
			r.mu.Lock()
			alreadyOffered := r.lastSeen == identity
			r.mu.Unlock()
			if alreadyOffered && r.validation.snapshot().State != RouteValidationRejected {
				return true
			}
		}
	}
	candidate, err := r.candidateFromState(ctx, state)
	if err != nil {
		r.validation.observe(Candidate{State: state})
		return false
	}
	identity := stateIdentity(state)
	identity.fleetRevision = candidate.FleetHead.Revision
	if identity.generationID == "" {
		return true
	}
	r.mu.Lock()
	if r.lastSeen == identity && (r.fleet == nil || r.validation.snapshot().State != RouteValidationRejected) {
		r.mu.Unlock()
		return true
	}
	r.lastSeen = identity
	r.mu.Unlock()
	r.validation.observe(candidate)
	select {
	case r.updates <- candidate:
		return true
	case <-ctx.Done():
		return false
	}
}

func stateIdentity(state starmap.CatalogState) catalogStateIdentity {
	return catalogStateIdentity{
		generationID:    state.GenerationID,
		payloadChecksum: state.PayloadChecksum,
	}
}

// notFound reports whether an error names an absent record.
func notFound(err error) bool {
	return errors.Is(err, starmaperrors.ErrNotFound)
}
