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
// Starmap publishes an effective generation into the candidate store; Starport
// then validates the candidate and advances its own accepted head, so a
// candidate that fails route validation never routes a request.
type Runtime struct {
	runtime *starmap.Runtime

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
	// observed. The accepted head is durable; this record says how the
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
	var acquirer starmap.Acquirer
	if settings.AcquisitionEnabled {
		built, err := acquisition.NewAcquirer(
			acquisition.WithAcquirerCredentialResolver(NewAcquisitionResolver(lookup)),
		)
		if err != nil {
			return nil, fmt.Errorf("open Starmap acquisition: %w", err)
		}
		acquirer = built
	}
	return openRuntime(ctx, store, settings, acquirer)
}

// openRuntime composes the connected runtime over one acquirer. It holds the
// whole composition, so the exported entry point states the credential plane
// alone and no caller repeats the wiring.
func openRuntime(
	ctx context.Context,
	store storage.KVStore,
	settings Settings,
	acquirer starmap.Acquirer,
) (*Runtime, error) {
	if ctx == nil {
		return nil, errors.New("catalog runtime context is required")
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
	acceptedClient, err := starmap.NewContext(ctx, starmap.WithCatalogStore(acceptedStore))
	if err != nil {
		return nil, fmt.Errorf("open accepted Starmap catalog: %w", err)
	}
	control, err := Open(acceptedClient)
	if err != nil {
		return nil, err
	}

	options := settings.starmapOptions()
	options = append(
		options,
		starmap.WithCatalogStore(candidateStore),
		starmap.WithLeaseStore(leases),
	)
	var cascade *remote.Source
	if settings.Source == string(starmap.SourceStarmap) {
		cascade, err = settings.cascadeSource(ctx)
		if err != nil {
			return nil, err
		}
		options = append(options, starmap.WithSource(cascade))
	}
	if acquirer != nil {
		options = append(options, starmap.WithAcquirer(acquirer))
	}
	connected, err := starmap.Open(ctx, options...)
	if err != nil {
		if cascade != nil {
			_ = cascade.Close()
		}
		return nil, fmt.Errorf("open Starmap runtime: %w", err)
	}
	return newRuntime(connected, cascade, candidateStore, acceptedStore, control, leases), nil
}

func newRuntime(
	connected *starmap.Runtime,
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
func (r *Runtime) Refresh(ctx context.Context) (starmap.RefreshReport, error) {
	if r == nil || r.runtime == nil {
		return starmap.RefreshReport{}, ErrCatalogSourceRequired
	}
	if ctx == nil {
		return starmap.RefreshReport{}, errors.New("catalog refresh context is required")
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
	epoch, err := r.leases.CurrentEpoch(ctx)
	if err != nil {
		return Candidate{}, err
	}
	candidate := Candidate{State: state, Epoch: epoch}
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
func (r *Runtime) Status() starmap.RuntimeStatus {
	if r == nil || r.runtime == nil {
		return starmap.RuntimeStatus{}
	}
	return r.runtime.Status()
}

// AcceptedGeneration returns the head Starport accepted. A deployment that
// accepted nothing yet reports the not-found error.
func (r *Runtime) AcceptedGeneration(ctx context.Context) (catalogs.Generation, error) {
	if r == nil || r.accepted == nil {
		return catalogs.Generation{}, ErrCatalogSourceRequired
	}
	return r.accepted.Current(ctx)
}

// Start begins forwarding every new candidate the connected runtime publishes.
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
	go r.forward(watchCtx, watch)
	return nil
}

// Updates emits every distinct candidate exactly once.
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

func (r *Runtime) forward(ctx context.Context, done chan struct{}) {
	defer close(done)
	updates := r.runtime.Updates()
	for {
		select {
		case <-ctx.Done():
			return
		case state, open := <-updates:
			if !open {
				return
			}
			r.offer(ctx, state)
		}
	}
}

func (r *Runtime) offer(ctx context.Context, state starmap.CatalogState) {
	identity := stateIdentity(state)
	if identity.generationID == "" {
		return
	}
	r.mu.Lock()
	if r.lastSeen == identity {
		r.mu.Unlock()
		return
	}
	r.lastSeen = identity
	r.mu.Unlock()

	epoch, err := r.leases.CurrentEpoch(ctx)
	if err != nil {
		// The candidate still reaches acceptance. The transaction reads the
		// epoch again and refuses a stale one, so an unreadable lease costs a
		// fence read here, never a wrong acceptance there.
		epoch = 0
	}
	candidate := Candidate{State: state, Epoch: epoch}
	r.validation.observe(candidate)

	// The offer waits for acceptance rather than dropping the candidate. A
	// dropped candidate would leave route validation with no record of a head
	// the source published, so only a closing runtime ends the wait.
	select {
	case r.updates <- candidate:
	case <-ctx.Done():
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
