package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/maphash"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

const starportCredentialProduct = "STARPORT"

var (
	// ErrResolverRequired reports an absent inference credential resolver.
	ErrResolverRequired = errors.New("inference credential resolver is required")
	// ErrProviderContractRequired reports absent inference credential metadata.
	ErrProviderContractRequired = errors.New("provider inference credential contract is required")
	// ErrProviderNotConfigured reports a selected provider without complete material.
	ErrProviderNotConfigured = errors.New("provider inference credentials are not configured")
	// ErrMaterialRevoked reports material invalidated while source work was in flight.
	ErrMaterialRevoked = errors.New("provider inference credential material was revoked")
)

// EnvironmentLookup reads one environment name without changing process state.
type EnvironmentLookup func(string) (string, bool)

// CloudChain resolves one compiled default-identity primitive.
type CloudChain interface {
	Resolve(
		context.Context,
		catalogs.ProviderCredentialProfile,
		map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
	) (SourceMaterial, error)
}

// CloudChainFunc adapts a function to one default-identity primitive.
type CloudChainFunc func(
	context.Context,
	catalogs.ProviderCredentialProfile,
	map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
) (SourceMaterial, error)

// MaterialSource resolves one configured provider's inference material.
type MaterialSource interface {
	ResolveMaterial(context.Context) (Material, error)
}

// Resolve implements CloudChain.
func (f CloudChainFunc) Resolve(
	ctx context.Context,
	profile catalogs.ProviderCredentialProfile,
	fields map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
) (SourceMaterial, error) {
	return f(ctx, profile, fields)
}

// ResolverOption configures inference credential resolution.
type ResolverOption func(*Resolver)

// WithEnvironmentLookup replaces process environment access.
func WithEnvironmentLookup(lookup EnvironmentLookup) ResolverOption {
	return func(resolver *Resolver) {
		if lookup != nil {
			resolver.lookup = lookup
			resolver.sources[ReferenceBackendEnvironment] = environmentSource{lookup: lookup}
		}
	}
}

// WithReferenceSource registers one typed explicit source primitive.
func WithReferenceSource(source ReferenceSource) ResolverOption {
	return func(resolver *Resolver) {
		if source != nil {
			resolver.sources[source.Backend()] = source
		}
	}
}

// WithCloudChain registers one typed default-identity primitive.
func WithCloudChain(
	primitive catalogs.ProviderAuthenticationPrimitive,
	chain CloudChain,
) ResolverOption {
	return func(resolver *Resolver) {
		if chain != nil {
			resolver.cloudChains[primitive] = chain
		}
	}
}

// WithResolverClock replaces wall-clock access for lifecycle tests.
func WithResolverClock(now func() time.Time) ResolverOption {
	return func(resolver *Resolver) {
		if now != nil {
			resolver.now = now
		}
	}
}

// Resolver owns inference credential source selection, caching, refresh, and
// single-flight work. It contains no provider roster.
type Resolver struct {
	lookup      EnvironmentLookup
	sources     map[ReferenceBackend]ReferenceSource
	cloudChains map[catalogs.ProviderAuthenticationPrimitive]CloudChain
	versionSeed maphash.Seed
	now         func() time.Time

	mu       sync.Mutex
	cache    map[string]Material
	inflight map[string]*resolutionCall
	epochs   map[string]uint64
}

type resolutionCall struct {
	done            chan struct{}
	material        Material
	configured      bool
	err             error
	retryForWaiters bool
	epoch           uint64
}

// NewResolver creates the built-in inference credential resolver.
func NewResolver(options ...ResolverOption) *Resolver {
	lookup := EnvironmentLookup(os.LookupEnv)
	resolver := &Resolver{
		lookup: lookup,
		sources: map[ReferenceBackend]ReferenceSource{
			ReferenceBackendEnvironment: environmentSource{lookup: lookup},
			ReferenceBackendFile:        fileSource{},
		},
		cloudChains: make(map[catalogs.ProviderAuthenticationPrimitive]CloudChain),
		versionSeed: maphash.MakeSeed(),
		now:         time.Now,
		cache:       make(map[string]Material),
		inflight:    make(map[string]*resolutionCall),
		epochs:      make(map[string]uint64),
	}
	for _, option := range options {
		if option != nil {
			option(resolver)
		}
	}
	return resolver
}

// ProviderHandle binds one provider contract and its deployment-owned source
// policies to a shared resolver.
type ProviderHandle struct {
	resolver *Resolver
	provider catalogs.Provider
	policies map[catalogs.ProviderCredentialFieldID]ReferencePolicy
	forced   bool
	identity string
}

// Provider creates one validated inference credential handle.
func (r *Resolver) Provider(
	provider catalogs.Provider,
	policies map[catalogs.ProviderCredentialFieldID]ReferencePolicy,
	forced bool,
) (*ProviderHandle, error) {
	if r == nil {
		return nil, ErrResolverRequired
	}
	if provider.Credentials == nil || len(provider.Credentials.Inference.Alternatives) == 0 {
		return nil, fmt.Errorf("%s: %w", provider.ID, ErrProviderContractRequired)
	}
	if err := provider.ValidateContract(); err != nil {
		return nil, fmt.Errorf("validate provider %s: %w", provider.ID, err)
	}
	fields := indexCredentialFields(provider.Credentials.Fields)
	for fieldID, policy := range policies {
		if _, exists := fields[fieldID]; !exists {
			return nil, &ReferenceError{Field: "field", Message: "does not reference a catalog field"}
		}
		if _, exists := r.sources[policy.Reference.backend]; !exists {
			return nil, &ReferenceError{Field: "backend", Message: "is not supported"}
		}
	}
	copiedPolicies := make(map[catalogs.ProviderCredentialFieldID]ReferencePolicy, len(policies))
	for fieldID, policy := range policies {
		copiedPolicies[fieldID] = policy
	}
	identity, err := r.providerIdentity(provider, copiedPolicies, forced)
	if err != nil {
		return nil, err
	}
	return &ProviderHandle{
		resolver: r, provider: catalogs.DeepCopyProvider(provider),
		policies: copiedPolicies, forced: forced, identity: identity,
	}, nil
}

// ResolveMaterial returns cached material when its lifecycle is fresh.
func (h *ProviderHandle) ResolveMaterial(ctx context.Context) (Material, error) {
	material, configured, err := h.resolve(ctx, false)
	if err != nil {
		return Material{}, err
	}
	if !configured {
		return Material{}, fmt.Errorf("%s: %w", h.provider.ID, ErrProviderNotConfigured)
	}
	return material, nil
}

// Resolve reports whether the provider has a configured inference profile.
func (h *ProviderHandle) Resolve(ctx context.Context) (Material, bool, error) {
	return h.resolve(ctx, false)
}

// Refresh forces one source resolution and atomically replaces cached
// material only after success.
func (h *ProviderHandle) Refresh(ctx context.Context) (Material, bool, error) {
	return h.resolve(ctx, true)
}

// Revoke invalidates cached material. A resolution that was already in flight
// cannot repopulate the cache after this call. The next resolution must read
// the configured source again.
func (h *ProviderHandle) Revoke() error {
	if h == nil || h.resolver == nil {
		return ErrResolverRequired
	}
	h.resolver.mu.Lock()
	h.resolver.epochs[h.identity]++
	delete(h.resolver.cache, h.identity)
	h.resolver.mu.Unlock()
	return nil
}

func (h *ProviderHandle) resolve(ctx context.Context, force bool) (Material, bool, error) {
	if h == nil || h.resolver == nil {
		return Material{}, false, ErrResolverRequired
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Material{}, false, err
	}
	return h.resolver.resolve(ctx, h, force)
}

func (r *Resolver) resolve(
	ctx context.Context,
	handle *ProviderHandle,
	force bool,
) (Material, bool, error) {
	for {
		r.mu.Lock()
		if !force {
			if cached, exists := r.cache[handle.identity]; exists && materialFresh(cached, r.now()) {
				r.mu.Unlock()
				return cached, true, nil
			}
		}
		if call, exists := r.inflight[handle.identity]; exists {
			r.mu.Unlock()
			select {
			case <-call.done:
				if err := ctx.Err(); err != nil {
					return Material{}, false, err
				}
				if call.retryForWaiters {
					continue
				}
				return call.material, call.configured, call.err
			case <-ctx.Done():
				return Material{}, false, ctx.Err()
			}
		}
		call := &resolutionCall{done: make(chan struct{}), epoch: r.epochs[handle.identity]}
		r.inflight[handle.identity] = call
		r.mu.Unlock()

		material, configured, resolveErr := r.resolveUncached(ctx, handle)
		retryForWaiters := resolveErr != nil && ctx.Err() != nil && errors.Is(resolveErr, ctx.Err())

		r.mu.Lock()
		if r.epochs[handle.identity] != call.epoch {
			material = Material{}
			configured = false
			resolveErr = ErrMaterialRevoked
			retryForWaiters = false
		}
		call.material = material
		call.configured = configured
		call.err = resolveErr
		call.retryForWaiters = retryForWaiters
		if call.err == nil && call.configured {
			r.cache[handle.identity] = call.material
		}
		delete(r.inflight, handle.identity)
		close(call.done)
		r.mu.Unlock()
		return call.material, call.configured, call.err
	}
}

func (r *Resolver) resolveUncached(
	ctx context.Context,
	handle *ProviderHandle,
) (Material, bool, error) {
	credentials := handle.provider.Credentials
	fields := indexCredentialFields(credentials.Fields)
	profiles := indexCredentialProfiles(credentials.Profiles)
	observedProvider := handle.forced
	for _, profileID := range credentials.Inference.Alternatives {
		profile := profiles[profileID]
		builder := newMaterialBuilder()
		missing := make(map[catalogs.ProviderCredentialFieldID]struct{})
		observedProfile := handle.forced
		for _, fieldID := range profile.Fields {
			resolved, selected, observed, err := r.resolveField(
				ctx,
				handle.provider.ID,
				fields[fieldID],
				handle.policies[fieldID],
			)
			if err != nil {
				return Material{}, false, err
			}
			observedProfile = observedProfile || observed
			observedProvider = observedProvider || observed
			if selected {
				builder.add(fieldID, resolved)
				continue
			}
			if fields[fieldID].Required {
				missing[fieldID] = struct{}{}
			}
		}
		if len(missing) > 0 && defaultChainPrimitive(profile.Primitive) && observedProfile {
			chain := r.cloudChains[profile.Primitive]
			if chain == nil {
				continue
			}
			chainMaterial, err := chain.Resolve(ctx, profile, fields)
			if err != nil {
				if IsSourceError(err, SourceErrorNotConfigured) {
					continue
				}
				return Material{}, false, err
			}
			for _, fieldID := range profile.Fields {
				if _, exists := builder.values[fieldID]; exists {
					continue
				}
				value, exists := chainMaterial.Value(string(fieldID))
				if !exists || value == "" {
					continue
				}
				if err := validateResolvedField(fields[fieldID], value); err != nil {
					return Material{}, false, err
				}
				builder.add(fieldID, resolvedFieldFromSource(value, chainMaterial))
			}
		}
		complete := true
		for _, fieldID := range profile.Fields {
			if fields[fieldID].Required {
				if _, exists := builder.values[fieldID]; !exists {
					complete = false
					break
				}
			}
		}
		if !complete || !observedProfile {
			continue
		}
		return builder.build(r, profile), true, nil
	}
	if observedProvider {
		return Material{}, false, fmt.Errorf("%s: %w", handle.provider.ID, ErrProviderNotConfigured)
	}
	return Material{}, false, nil
}

type resolvedField struct {
	value     string
	version   string
	expiresAt time.Time
	lease     *Lease
}

func (r *Resolver) resolveField(
	ctx context.Context,
	providerID catalogs.ProviderID,
	field catalogs.ProviderCredentialField,
	policy ReferencePolicy,
) (resolvedField, bool, bool, error) {
	if policy.Reference.backend != "" {
		source := r.sources[policy.Reference.backend]
		material, err := source.Resolve(ctx, policy.Reference)
		if err == nil {
			value, selectErr := referenceValue(material, policy.Reference)
			if selectErr != nil {
				return resolvedField{}, false, true, selectErr
			}
			if validateErr := validateResolvedField(field, value); validateErr != nil {
				return resolvedField{}, false, true, validateErr
			}
			return resolvedFieldFromSource(value, material), true, true, nil
		}
		if !policy.FallbackAmbient || !IsSourceError(err, SourceErrorNotConfigured) {
			return resolvedField{}, false, true, err
		}
	}
	return r.resolveAmbientField(providerID, field)
}

func (r *Resolver) resolveAmbientField(
	providerID catalogs.ProviderID,
	field catalogs.ProviderCredentialField,
) (resolvedField, bool, bool, error) {
	candidates := append([]string(nil), field.Environment...)
	derived, err := catalogs.DerivedCredentialEnvironmentName(
		starportCredentialProduct,
		providerID,
		field.ID,
	)
	if err != nil {
		return resolvedField{}, false, false, err
	}
	candidates = append(candidates, derived)
	for _, name := range candidates {
		value, exists := r.lookup(name)
		if !exists || value == "" {
			continue
		}
		if err := validateResolvedField(field, value); err != nil {
			return resolvedField{}, false, true, &SelectedValueError{
				Environment: name, ProviderID: providerID, FieldID: field.ID,
			}
		}
		return resolvedField{value: value, version: name + "\x00" + value}, true, true, nil
	}
	if field.Default != "" {
		return resolvedField{value: field.Default, version: "default\x00" + field.Default}, true, false, nil
	}
	return resolvedField{}, false, false, nil
}

// SelectedValueError reports a selected invalid ambient value without the value.
type SelectedValueError struct {
	Environment string
	ProviderID  catalogs.ProviderID
	FieldID     catalogs.ProviderCredentialFieldID
}

func (e *SelectedValueError) Error() string {
	return fmt.Sprintf("%s selected an invalid value for %s/%s", e.Environment, e.ProviderID, e.FieldID)
}

func validateResolvedField(field catalogs.ProviderCredentialField, value string) error {
	if field.Pattern == "" {
		return nil
	}
	matched, err := regexp.MatchString(field.Pattern, value)
	if err != nil || !matched {
		return &SelectedValueError{FieldID: field.ID}
	}
	return nil
}

func referenceValue(material SourceMaterial, reference Reference) (string, error) {
	if reference.field != "" {
		value, exists := material.Value(reference.field)
		if !exists || value == "" {
			return "", NewSourceError(SourceErrorNotConfigured, reference.backend)
		}
		return value, nil
	}
	if value, exists := material.Value("value"); exists && value != "" {
		return value, nil
	}
	if len(material.values) == 1 {
		for _, value := range material.values {
			if value != "" {
				return value, nil
			}
		}
	}
	return "", NewSourceError(SourceErrorInvalid, reference.backend)
}

func resolvedFieldFromSource(value string, material SourceMaterial) resolvedField {
	var lease *Lease
	if material.lease != nil {
		copied := *material.lease
		lease = &copied
	}
	return resolvedField{
		value: value, version: material.version,
		expiresAt: material.expiresAt, lease: lease,
	}
}

type materialBuilder struct {
	values   map[catalogs.ProviderCredentialFieldID]string
	versions map[catalogs.ProviderCredentialFieldID]string
	expires  time.Time
	lease    *Lease
}

func newMaterialBuilder() *materialBuilder {
	return &materialBuilder{
		values:   make(map[catalogs.ProviderCredentialFieldID]string),
		versions: make(map[catalogs.ProviderCredentialFieldID]string),
	}
}

func (b *materialBuilder) add(fieldID catalogs.ProviderCredentialFieldID, resolved resolvedField) {
	b.values[fieldID] = resolved.value
	b.versions[fieldID] = resolved.version
	if !resolved.expiresAt.IsZero() && (b.expires.IsZero() || resolved.expiresAt.Before(b.expires)) {
		b.expires = resolved.expiresAt
	}
	if resolved.lease == nil {
		return
	}
	if b.lease == nil {
		lease := *resolved.lease
		b.lease = &lease
		return
	}
	b.lease.Renewable = b.lease.Renewable || resolved.lease.Renewable
	if b.lease.RefreshAfter.IsZero() ||
		(!resolved.lease.RefreshAfter.IsZero() && resolved.lease.RefreshAfter.Before(b.lease.RefreshAfter)) {
		b.lease.RefreshAfter = resolved.lease.RefreshAfter
	}
}

func (b *materialBuilder) build(resolver *Resolver, profile catalogs.ProviderCredentialProfile) Material {
	fieldIDs := make([]string, 0, len(b.values))
	for fieldID := range b.values {
		fieldIDs = append(fieldIDs, string(fieldID))
	}
	sort.Strings(fieldIDs)
	versionParts := make([]string, 0, 3+3*len(fieldIDs))
	versionParts = append(versionParts, "material", string(profile.ID), string(profile.Primitive))
	for _, fieldValue := range fieldIDs {
		fieldID := catalogs.ProviderCredentialFieldID(fieldValue)
		versionParts = append(versionParts, fieldValue, b.versions[fieldID], b.values[fieldID])
	}
	return NewMaterial(profile, b.values, MaterialMetadata{
		Version: resolver.opaqueVersion(versionParts...), ExpiresAt: b.expires, Lease: b.lease,
	})
}

func materialFresh(material Material, now time.Time) bool {
	if material.Empty() {
		return false
	}
	if expiresAt, exists := material.ExpiresAt(); exists && !now.Before(expiresAt) {
		return false
	}
	if lease, exists := material.Lease(); exists && !lease.RefreshAfter.IsZero() &&
		!now.Before(lease.RefreshAfter) {
		return false
	}
	return true
}

func defaultChainPrimitive(primitive catalogs.ProviderAuthenticationPrimitive) bool {
	switch primitive {
	case catalogs.ProviderAuthenticationGoogleDefault,
		catalogs.ProviderAuthenticationAzureDefault,
		catalogs.ProviderAuthenticationAWSDefault:
		return true
	default:
		return false
	}
}

func indexCredentialFields(
	fields []catalogs.ProviderCredentialField,
) map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField {
	indexed := make(map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField, len(fields))
	for _, field := range fields {
		indexed[field.ID] = field
	}
	return indexed
}

func indexCredentialProfiles(
	profiles []catalogs.ProviderCredentialProfile,
) map[catalogs.ProviderCredentialProfileID]catalogs.ProviderCredentialProfile {
	indexed := make(map[catalogs.ProviderCredentialProfileID]catalogs.ProviderCredentialProfile, len(profiles))
	for _, profile := range profiles {
		indexed[profile.ID] = profile
	}
	return indexed
}

func (r *Resolver) providerIdentity(
	provider catalogs.Provider,
	policies map[catalogs.ProviderCredentialFieldID]ReferencePolicy,
	forced bool,
) (string, error) {
	encoded, err := json.Marshal(provider.Credentials)
	if err != nil {
		return "", fmt.Errorf("encode provider credential contract: %w", err)
	}
	parts := []string{"provider", string(provider.ID), string(encoded), fmt.Sprint(forced)}
	fieldIDs := make([]string, 0, len(policies))
	for fieldID := range policies {
		fieldIDs = append(fieldIDs, string(fieldID))
	}
	sort.Strings(fieldIDs)
	for _, fieldValue := range fieldIDs {
		policy := policies[catalogs.ProviderCredentialFieldID(fieldValue)]
		parts = append(parts,
			fieldValue,
			string(policy.Reference.backend),
			policy.Reference.resource,
			policy.Reference.field,
			policy.Reference.version,
			fmt.Sprint(policy.FallbackAmbient),
		)
	}
	return r.opaqueVersion(parts...), nil
}

func (r *Resolver) opaqueVersion(parts ...string) string {
	var hash maphash.Hash
	hash.SetSeed(r.versionSeed)
	for _, part := range parts {
		_, _ = hash.WriteString(part)
		_, _ = hash.Write([]byte{0})
	}
	return fmt.Sprintf("v1-%016x", hash.Sum64())
}
