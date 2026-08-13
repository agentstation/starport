package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	pkgsync "github.com/agentstation/starmap/pkg/sync"
	"github.com/agentstation/starmap/remote"

	"github.com/agentstation/starport/internal/storage"
)

const (
	// DefaultRemoteActivationInterval bounds propagation from Starmap's atomic
	// subscriber state into Starport's runtime transaction. It causes no network
	// request.
	DefaultRemoteActivationInterval = 250 * time.Millisecond
	// DefaultRemoteFetchTimeout bounds manifest and payload requests. The SSE
	// stream uses Starmap's heartbeat and liveness contract instead.
	DefaultRemoteFetchTimeout  = 2 * time.Minute
	remoteAPIKeyHeader         = "X-API-Key" // #nosec G101 -- Public HTTP header name, not credential material.
	remoteEventStreamMediaType = "text/event-stream"
)

// RemoteConfig defines one verified Starmap publication source.
type RemoteConfig struct {
	BaseURL            string
	APIKey             string
	ActivationInterval time.Duration
	FetchTimeout       time.Duration
	HTTPClient         *http.Client
}

type remoteSubscriber interface {
	Start(context.Context) error
	Close() error
	State() starmap.CatalogState
}

type catalogStateIdentity struct {
	generationID    string
	payloadChecksum string
}

// RemoteRuntime owns the verified Starmap subscriber, its durable remote head,
// and Starport's separate accepted runtime generation.
type RemoteRuntime struct {
	subscriber remoteSubscriber
	remote     *GenerationStore
	accepted   *GenerationStore
	control    *ControlPlane
	interval   time.Duration
	updates    chan starmap.CatalogState

	mu           sync.Mutex
	started      bool
	cancel       context.CancelFunc
	monitorDone  chan struct{}
	lastObserved catalogStateIdentity
}

// OpenRemoteRuntime creates an idle remote runtime without starting network or
// background work.
func OpenRemoteRuntime(
	ctx context.Context,
	store storage.KVStore,
	config RemoteConfig,
) (*RemoteRuntime, error) {
	acceptedStore, err := NewGenerationStore(store)
	if err != nil {
		return nil, err
	}
	acceptedClient, err := starmap.NewContext(
		ctx,
		starmap.WithCatalogStore(acceptedStore),
	)
	if err != nil {
		return nil, fmt.Errorf("open accepted Starmap catalog: %w", err)
	}
	control, err := Open(acceptedClient)
	if err != nil {
		return nil, err
	}
	remoteStore, err := newRemoteGenerationStore(store)
	if err != nil {
		return nil, err
	}

	var pinned *catalogs.Generation
	acceptedGeneration, currentErr := acceptedStore.Current(ctx)
	switch {
	case currentErr == nil:
		pinnedGeneration := acceptedGeneration.Copy()
		pinned = &pinnedGeneration
	case errors.Is(currentErr, starmaperrors.ErrNotFound):
	case currentErr != nil:
		return nil, fmt.Errorf("read accepted catalog generation: %w", currentErr)
	}

	interval := config.ActivationInterval
	if interval == 0 {
		interval = DefaultRemoteActivationInterval
	}
	if interval < 0 {
		return nil, errors.New("remote catalog activation interval cannot be negative")
	}
	fetchTimeout := config.FetchTimeout
	if fetchTimeout == 0 {
		fetchTimeout = DefaultRemoteFetchTimeout
	}
	if fetchTimeout < 0 {
		return nil, errors.New("remote catalog fetch timeout cannot be negative")
	}
	httpClient := remoteHTTPClient(
		config.HTTPClient,
		config.APIKey,
		fetchTimeout,
	)
	subscriber, err := remote.NewContext(ctx, remote.Config{
		BaseURL:         config.BaseURL,
		HTTPClient:      httpClient,
		CatalogStore:    remoteStore,
		PinnedBootstrap: pinned,
	})
	if err != nil {
		return nil, fmt.Errorf("open remote Starmap subscriber: %w", err)
	}
	return newRemoteRuntime(subscriber, remoteStore, acceptedStore, control, interval), nil
}

func newRemoteRuntime(
	subscriber remoteSubscriber,
	remoteStore *GenerationStore,
	acceptedStore *GenerationStore,
	control *ControlPlane,
	interval time.Duration,
) *RemoteRuntime {
	return &RemoteRuntime{
		subscriber: subscriber,
		remote:     remoteStore,
		accepted:   acceptedStore,
		control:    control,
		interval:   interval,
		updates:    make(chan starmap.CatalogState, 1),
	}
}

// ControlPlane returns the last accepted Starport runtime catalog.
func (r *RemoteRuntime) ControlPlane() *ControlPlane {
	if r == nil {
		return nil
	}
	return r.control
}

// Sync returns the current verified subscriber state without network I/O.
// Remote publication and retry work belongs to Start.
func (r *RemoteRuntime) Sync(
	ctx context.Context,
	_ ...pkgsync.Option,
) (*pkgsync.Result, starmap.CatalogState, error) {
	if r == nil || r.subscriber == nil {
		return nil, starmap.CatalogState{}, ErrCatalogSourceRequired
	}
	if ctx == nil {
		return nil, starmap.CatalogState{}, errors.New("remote catalog context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, starmap.CatalogState{}, err
	}
	state := r.subscriber.State()
	return &pkgsync.Result{GenerationID: state.GenerationID}, state, nil
}

// Start starts the Starmap remote lifecycle and the local atomic-state sampler.
func (r *RemoteRuntime) Start(ctx context.Context) error {
	if r == nil || r.subscriber == nil {
		return ErrCatalogSourceRequired
	}
	if ctx == nil {
		return errors.New("remote catalog context is required")
	}
	if r.interval <= 0 {
		return errors.New("remote catalog activation interval must be positive")
	}

	runCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		cancel()
		return errors.New("remote catalog runtime already started")
	}
	r.started = true
	r.cancel = cancel
	r.monitorDone = make(chan struct{})
	r.mu.Unlock()

	if err := r.subscriber.Start(runCtx); err != nil {
		cancel()
		r.mu.Lock()
		r.started = false
		r.cancel = nil
		close(r.monitorDone)
		r.monitorDone = nil
		r.mu.Unlock()
		return err
	}
	r.mu.Lock()
	r.lastObserved = stateIdentity(r.subscriber.State())
	done := r.monitorDone
	r.mu.Unlock()
	go r.monitor(runCtx, done)
	return nil
}

// CurrentCandidate returns the subscriber's current atomic state without I/O.
func (r *RemoteRuntime) CurrentCandidate() starmap.CatalogState {
	if r == nil || r.subscriber == nil {
		return starmap.CatalogState{}
	}
	return r.subscriber.State()
}

// Updates returns verified atomic states after the initial candidate.
func (r *RemoteRuntime) Updates() <-chan starmap.CatalogState {
	if r == nil {
		return nil
	}
	return r.updates
}

// Accept records a generation after Starport builds and validates the complete
// runtime candidate. The remote and accepted current pointers remain
// independent.
func (r *RemoteRuntime) Accept(ctx context.Context, state starmap.CatalogState) error {
	if r == nil || r.remote == nil || r.accepted == nil {
		return ErrCatalogSourceRequired
	}
	if ctx == nil {
		return errors.New("remote catalog context is required")
	}
	if state.Catalog == nil || state.GenerationID == "" {
		return ErrCatalogRequired
	}
	generation, err := r.remote.Get(ctx, state.GenerationID)
	if err != nil {
		return fmt.Errorf("read remote catalog generation for acceptance: %w", err)
	}
	if generation.Manifest.Payload.Checksum != state.PayloadChecksum {
		return &starmaperrors.ConflictError{
			Resource: "remote catalog generation",
			Expected: state.PayloadChecksum,
			Actual:   generation.Manifest.Payload.Checksum,
			Message:  "atomic state does not match the durable generation",
		}
	}
	if !generation.Manifest.GeneratedAt.Equal(state.GeneratedAt) {
		return &starmaperrors.ConflictError{
			Resource: "remote catalog generation timestamp",
			Expected: state.GeneratedAt.Format(time.RFC3339Nano),
			Actual:   generation.Manifest.GeneratedAt.Format(time.RFC3339Nano),
		}
	}

	expectedID := ""
	current, currentErr := r.accepted.Current(ctx)
	switch {
	case currentErr == nil:
		expectedID = current.Manifest.GenerationID
		if expectedID == state.GenerationID {
			return nil
		}
		if generation.Manifest.GeneratedAt.Before(current.Manifest.GeneratedAt) {
			return &starmaperrors.ConflictError{
				Resource: "accepted catalog generation order",
				Expected: current.Manifest.GeneratedAt.Format(time.RFC3339Nano),
				Actual:   generation.Manifest.GeneratedAt.Format(time.RFC3339Nano),
				Message:  "an accepted generation cannot move backward",
			}
		}
		if generation.Manifest.GeneratedAt.Equal(current.Manifest.GeneratedAt) &&
			generation.Manifest.Payload.Checksum != current.Manifest.Payload.Checksum {
			return &starmaperrors.ConflictError{
				Resource: "accepted catalog generation order",
				Expected: current.Manifest.Payload.Checksum,
				Actual:   generation.Manifest.Payload.Checksum,
				Message:  "distinct payloads cannot share an accepted generation timestamp",
			}
		}
	case errors.Is(currentErr, starmaperrors.ErrNotFound):
	case currentErr != nil:
		return fmt.Errorf("read accepted catalog generation: %w", currentErr)
	}
	if err := r.accepted.Commit(ctx, generation, expectedID); err != nil {
		return fmt.Errorf("accept remote catalog generation: %w", err)
	}
	return nil
}

// Close stops and joins all remote runtime work.
func (r *RemoteRuntime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	cancel := r.cancel
	done := r.monitorDone
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	closeErr := r.subscriber.Close()
	if done == nil {
		return closeErr
	}
	if ctx == nil {
		return errors.Join(closeErr, errors.New("remote catalog close context is required"))
	}
	select {
	case <-done:
		return closeErr
	case <-ctx.Done():
		return errors.Join(closeErr, ctx.Err())
	}
}

func (r *RemoteRuntime) monitor(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state := r.subscriber.State()
			identity := stateIdentity(state)
			r.mu.Lock()
			changed := identity != r.lastObserved
			if changed {
				r.lastObserved = identity
			}
			r.mu.Unlock()
			if changed {
				r.offerLatest(state)
			}
		}
	}
}

func (r *RemoteRuntime) offerLatest(state starmap.CatalogState) {
	select {
	case r.updates <- state:
		return
	default:
	}
	select {
	case <-r.updates:
	default:
	}
	select {
	case r.updates <- state:
	default:
	}
}

func stateIdentity(state starmap.CatalogState) catalogStateIdentity {
	return catalogStateIdentity{
		generationID:    state.GenerationID,
		payloadChecksum: state.PayloadChecksum,
	}
}

func remoteHTTPClient(
	source *http.Client,
	apiKey string,
	fetchTimeout time.Duration,
) *http.Client {
	if source == nil {
		source = http.DefaultClient
	}
	client := *source
	client.Timeout = 0
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = &catalogRemoteTransport{
		base: transport, apiKey: apiKey, fetchTimeout: fetchTimeout,
	}
	return &client
}

type catalogRemoteTransport struct {
	base         http.RoundTripper
	apiKey       string
	fetchTimeout time.Duration
}

func (t *catalogRemoteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	if t.apiKey != "" {
		clone.Header.Set(remoteAPIKeyHeader, t.apiKey)
	}
	if clone.Header.Get("Accept") == remoteEventStreamMediaType ||
		t.fetchTimeout <= 0 {
		return t.base.RoundTrip(clone)
	}
	ctx, cancel := context.WithTimeout(clone.Context(), t.fetchTimeout)
	clone = clone.WithContext(ctx)
	response, err := t.base.RoundTrip(clone)
	if err != nil {
		cancel()
		return nil, err
	}
	if response == nil || response.Body == nil {
		cancel()
		return response, nil
	}
	response.Body = &cancelOnCloseBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body *cancelOnCloseBody) Close() error {
	body.cancel()
	return body.ReadCloser.Close()
}

var _ remoteSubscriber = (*remote.Subscriber)(nil)
