package providers

import (
	"context"
	"errors"
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/config"
)

var (
	// ErrReconcilerRequired reports an absent provider reconciler dependency.
	ErrReconcilerRequired = errors.New("provider reconciler dependency is required")
	// ErrCatalogViewRequired reports an incomplete catalog generation view.
	ErrCatalogViewRequired = errors.New("provider reconciliation catalog view is required")
)

// DefaultReconcileTimeout bounds one provider source operation when the
// caller does not configure a deployment-specific deadline.
const DefaultReconcileTimeout = 10 * time.Second

// CatalogView binds provider contracts to the immutable Starmap generation
// from which they came.
type CatalogView struct {
	GenerationID    string
	PayloadChecksum string
	Providers       []catalogs.Provider
}

// CatalogSource returns the current immutable provider contract view.
type CatalogSource func() (CatalogView, error)

// RuntimePublisher atomically publishes provider configuration against the
// exact catalog view used to resolve it.
type RuntimePublisher func(context.Context, CatalogView, config.ProvidersConfig) error

// CredentialRuntimeResolver resolves deployment material from Starmap-owned
// inference credential contracts.
type CredentialRuntimeResolver interface {
	ValidateProviderCredentialContracts([]catalogs.Provider) error
	ResolveProviderRuntime(
		context.Context,
		catalogs.Provider,
		config.ProviderConfig,
		bool,
	) (config.ProviderConfig, bool, error)
}

// ReconcileFailure records one isolated provider-local failure.
type ReconcileFailure struct {
	ProviderID catalogs.ProviderID
	Err        error
}

// ReconcileReport describes one completed provider reconciliation without
// exposing credential material.
type ReconcileReport struct {
	Revision            uint64
	Changed             bool
	ConfiguredProviders []catalogs.ProviderID
	Failures            []ReconcileFailure
}

// Reconciler resolves all catalog providers outside the inference hot path and
// publishes only an effective configuration change.
type Reconciler struct {
	source   CatalogSource
	resolver CredentialRuntimeResolver
	settings config.ProvidersConfig
	publish  RuntimePublisher
	timeout  time.Duration

	stateMu  sync.RWMutex
	view     CatalogView
	current  config.ProvidersConfig
	revision uint64

	flightMu sync.Mutex
	inflight *reconcileCall
}

type reconcileCall struct {
	done            chan struct{}
	force           bool
	waiters         int
	report          ReconcileReport
	err             error
	retryForWaiters bool
}

// NewReconciler creates a provider reconciliation boundary. Timeout applies to
// each provider independently, so one provider cannot consume another
// provider's resolution window.
func NewReconciler(
	source CatalogSource,
	resolver CredentialRuntimeResolver,
	settings config.ProvidersConfig,
	publish RuntimePublisher,
	timeout time.Duration,
) (*Reconciler, error) {
	if source == nil || resolver == nil || publish == nil {
		return nil, ErrReconcilerRequired
	}
	if timeout < 0 {
		return nil, errors.New("provider reconcile timeout cannot be negative")
	}
	if timeout == 0 {
		timeout = DefaultReconcileTimeout
	}
	return &Reconciler{
		source: source, resolver: resolver,
		settings: config.CloneProvidersConfig(settings),
		publish:  publish, timeout: timeout,
	}, nil
}

// Adopt records a configuration that another complete runtime transaction
// already published, such as startup or a catalog generation change.
func (r *Reconciler) Adopt(view CatalogView, current config.ProvidersConfig) error {
	if r == nil {
		return ErrReconcilerRequired
	}
	if err := validateCatalogView(view); err != nil {
		return err
	}
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if r.revision == 0 || !sameCatalogView(r.view, view) ||
		!equivalentProviderConfigs(r.current, current) {
		r.revision++
	}
	r.view = cloneCatalogView(view)
	r.current = config.CloneProvidersConfig(current)
	return nil
}

// Reconcile shares concurrent work. A forced manual reconciliation waits for
// an in-flight interval pass and retries if that pass did not force sources.
func (r *Reconciler) Reconcile(
	ctx context.Context,
	force bool,
) (ReconcileReport, error) {
	if r == nil {
		return ReconcileReport{}, ErrReconcilerRequired
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return ReconcileReport{}, err
		}
		r.flightMu.Lock()
		if call := r.inflight; call != nil {
			call.waiters++
			r.flightMu.Unlock()
			select {
			case <-call.done:
				if err := ctx.Err(); err != nil {
					return ReconcileReport{}, err
				}
				if call.retryForWaiters || force && !call.force {
					continue
				}
				return cloneReconcileReport(call.report), call.err
			case <-ctx.Done():
				return ReconcileReport{}, ctx.Err()
			}
		}
		call := &reconcileCall{done: make(chan struct{}), force: force}
		r.inflight = call
		r.flightMu.Unlock()

		call.report, call.err = r.reconcile(ctx, force)
		call.retryForWaiters = call.err != nil && ctx.Err() != nil

		r.flightMu.Lock()
		r.inflight = nil
		close(call.done)
		r.flightMu.Unlock()
		return cloneReconcileReport(call.report), call.err
	}
}

// Snapshot returns the latest accepted provider reconciliation state.
func (r *Reconciler) Snapshot() ReconcileReport {
	if r == nil {
		return ReconcileReport{}
	}
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return reportFor(r.revision, false, r.current, nil)
}

func (r *Reconciler) reconcile(
	ctx context.Context,
	force bool,
) (ReconcileReport, error) {
	view, err := r.source()
	if err != nil {
		return ReconcileReport{}, err
	}
	if err := validateCatalogView(view); err != nil {
		return ReconcileReport{}, err
	}
	if err := r.resolver.ValidateProviderCredentialContracts(view.Providers); err != nil {
		return ReconcileReport{}, err
	}

	r.stateMu.RLock()
	priorView := cloneCatalogView(r.view)
	prior := config.CloneProvidersConfig(r.current)
	priorRevision := r.revision
	r.stateMu.RUnlock()
	retainPrior := sameCatalogView(priorView, view)

	type providerResult struct {
		providerID catalogs.ProviderID
		config     config.ProviderConfig
		configured bool
		err        error
	}
	results := make([]providerResult, len(view.Providers))
	var wait sync.WaitGroup
	for index := range view.Providers {
		index := index
		provider := view.Providers[index]
		wait.Add(1)
		go func() {
			defer wait.Done()
			providerCtx, cancel := context.WithTimeout(ctx, r.timeout)
			defer cancel()
			resolved, configured, resolveErr := r.resolver.ResolveProviderRuntime(
				providerCtx,
				provider,
				r.settings[provider.ID],
				force,
			)
			results[index] = providerResult{
				providerID: provider.ID,
				config:     resolved, configured: configured, err: resolveErr,
			}
		}()
	}
	wait.Wait()
	if err := ctx.Err(); err != nil {
		return ReconcileReport{}, err
	}

	next := make(config.ProvidersConfig)
	failures := make([]ReconcileFailure, 0)
	for _, result := range results {
		if result.err != nil {
			failures = append(failures, ReconcileFailure{
				ProviderID: result.providerID,
				Err:        result.err,
			})
			if retainPrior {
				if retained, exists := prior[result.providerID]; exists {
					next[result.providerID] = retained
				}
			}
			continue
		}
		if result.configured {
			next[result.providerID] = result.config
		}
	}
	if err := next.Validate(); err != nil {
		return ReconcileReport{}, err
	}
	changed := !sameCatalogView(priorView, view) ||
		!equivalentProviderConfigs(prior, next)
	if !changed {
		return reportFor(priorRevision, false, next, failures), nil
	}
	if err := r.publish(ctx, view, next); err != nil {
		return ReconcileReport{}, err
	}

	r.stateMu.Lock()
	r.view = cloneCatalogView(view)
	r.current = config.CloneProvidersConfig(next)
	r.revision++
	revision := r.revision
	r.stateMu.Unlock()
	return reportFor(revision, true, next, failures), nil
}

func equivalentProviderConfigs(left, right config.ProvidersConfig) bool {
	if len(left) != len(right) {
		return false
	}
	for providerID, leftProvider := range left {
		rightProvider, exists := right[providerID]
		if !exists || !equivalentProviderConfig(leftProvider, rightProvider) {
			return false
		}
	}
	return true
}

func equivalentProviderConfig(left, right config.ProviderConfig) bool {
	leftProfile := left.Material.Profile()
	rightProfile := right.Material.Profile()
	if left.BaseURL != right.BaseURL || left.Timeout != right.Timeout ||
		left.MaxConnections != right.MaxConnections || left.Enabled != right.Enabled ||
		leftProfile.ID != rightProfile.ID || leftProfile.Primitive != rightProfile.Primitive {
		return false
	}
	return maps.Equal(left.Material.EndpointBindings(), right.Material.EndpointBindings()) &&
		maps.Equal(left.CredentialReferences, right.CredentialReferences)
}

func reportFor(
	revision uint64,
	changed bool,
	configs config.ProvidersConfig,
	failures []ReconcileFailure,
) ReconcileReport {
	providerIDs := make([]catalogs.ProviderID, 0, len(configs))
	for providerID := range configs {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Slice(providerIDs, func(left, right int) bool {
		return providerIDs[left] < providerIDs[right]
	})
	return ReconcileReport{
		Revision: revision, Changed: changed,
		ConfiguredProviders: providerIDs,
		Failures:            append([]ReconcileFailure(nil), failures...),
	}
}

func cloneReconcileReport(report ReconcileReport) ReconcileReport {
	report.ConfiguredProviders = append(
		[]catalogs.ProviderID(nil),
		report.ConfiguredProviders...,
	)
	report.Failures = append([]ReconcileFailure(nil), report.Failures...)
	return report
}

func validateCatalogView(view CatalogView) error {
	if view.GenerationID == "" || view.PayloadChecksum == "" || view.Providers == nil {
		return ErrCatalogViewRequired
	}
	return nil
}

func sameCatalogView(left, right CatalogView) bool {
	return left.GenerationID == right.GenerationID &&
		left.PayloadChecksum == right.PayloadChecksum
}

func cloneCatalogView(view CatalogView) CatalogView {
	result := view
	result.Providers = make([]catalogs.Provider, len(view.Providers))
	for index := range view.Providers {
		result.Providers[index] = catalogs.DeepCopyProvider(view.Providers[index])
	}
	return result
}
