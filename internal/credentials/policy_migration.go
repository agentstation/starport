package credentials

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// PolicyConflictError refuses a changed deployment credential during migration.
// Names identify environment selections and never contain credential values.
type PolicyConflictError struct {
	Provider catalogs.ProviderID
	Names    []string
}

func (e *PolicyConflictError) Error() string {
	return fmt.Sprintf("inference credential selections differ for %s (%s). Select an explicit inference reference or remove the conflicting variable", e.Provider, strings.Join(e.Names, ", "))
}

func (r *Resolver) resolveUncached(ctx context.Context, handle *ProviderHandle, allowCloudChain, allowRemoteReferences bool) (Material, bool, error) {
	policy := r.environmentPolicy
	if r.selectionPolicy != nil {
		if policy != InferencePolicyCurrent {
			return Material{}, false, fmt.Errorf("inference migration requires the current policy")
		}
		retained, err := r.selectionPolicy.Policy(ctx, handle.provider.ID)
		if err != nil {
			return Material{}, false, err
		}
		if retained != InferencePolicyCurrent && retained != InferencePolicyLegacy {
			return Material{}, false, fmt.Errorf("unsupported retained inference policy")
		}
		policy = retained
	}
	inputs := newPolicyEvaluation(r)
	material, configured, err := inputs.resolver(r.environmentPolicy).resolveProfiles(ctx, handle, allowCloudChain, allowRemoteReferences)
	if err != nil || !configured {
		return material, configured, err
	}
	if policy == InferencePolicyLegacy && r.selectionPolicy != nil {
		previous, wasConfigured, err := inputs.resolver(InferencePolicyLegacy).resolveProfiles(ctx, handle, allowCloudChain, allowRemoteReferences)
		if err != nil {
			return Material{}, false, err
		}
		if wasConfigured && !sameSelection(previous, material) {
			return Material{}, false, &PolicyConflictError{Provider: handle.provider.ID, Names: inputs.selectedNames()}
		}
		if err := r.selectionPolicy.Accept(ctx, handle.provider.ID); err != nil {
			return Material{}, false, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Material{}, false, err
	}
	if !materialUsable(material, r.now()) {
		return Material{}, false, NewSourceError(SourceErrorUnavailable, "inference")
	}
	return material, true, nil
}

func sameSelection(left, right Material) bool {
	return reflect.DeepEqual(left.profile, right.profile) && reflect.DeepEqual(left.values, right.values)
}

type capturedEnvironment struct {
	value   string
	present bool
}
type capturedSource struct {
	material SourceMaterial
	err      error
}

type policyEvaluation struct {
	base        *Resolver
	environment map[string]capturedEnvironment
	sources     map[ReferenceBackend]ReferenceSource
	chains      map[catalogs.ProviderAuthenticationPrimitive]CloudChain
}

func newPolicyEvaluation(base *Resolver) *policyEvaluation {
	inputs := &policyEvaluation{base: base, environment: make(map[string]capturedEnvironment), sources: make(map[ReferenceBackend]ReferenceSource), chains: make(map[catalogs.ProviderAuthenticationPrimitive]CloudChain)}
	for backend, source := range base.sources {
		if backend == ReferenceBackendEnvironment {
			source = environmentSource{lookup: inputs.lookup}
		}
		inputs.sources[backend] = &policySourceSnapshot{source: source, results: make(map[Reference]capturedSource)}
	}
	for primitive, chain := range base.cloudChains {
		inputs.chains[primitive] = &policyChainSnapshot{CloudChain: chain, results: make(map[catalogs.ProviderCredentialProfileID]capturedSource)}
	}
	return inputs
}

func (p *policyEvaluation) lookup(name string) (string, bool) {
	if value, exists := p.environment[name]; exists {
		return value.value, value.present
	}
	value, present := p.base.lookup(name)
	p.environment[name] = capturedEnvironment{value, present}
	return value, present
}

func (p *policyEvaluation) resolver(policy EnvironmentPolicy) *Resolver {
	r := NewResolver(WithEnvironmentLookup(p.lookup), WithEnvironmentPolicy(policy), WithStarmapFallback(p.base.starmapFallback), WithResolverClock(p.base.now), WithDirectSecretRefreshInterval(p.base.directSecretRefreshInterval))
	r.sources = p.sources
	r.cloudChains = p.chains
	r.versionSeed = p.base.versionSeed
	return r
}

func (p *policyEvaluation) selectedNames() []string {
	var names []string
	for name, value := range p.environment {
		if value.present && value.value != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

type policySourceSnapshot struct {
	source  ReferenceSource
	results map[Reference]capturedSource
}

func (s *policySourceSnapshot) Backend() ReferenceBackend { return s.source.Backend() }
func (s *policySourceSnapshot) Resolve(ctx context.Context, ref Reference) (SourceMaterial, error) {
	if value, exists := s.results[ref]; exists {
		return value.material, value.err
	}
	material, err := s.source.Resolve(ctx, ref)
	if err == nil {
		for prior, result := range s.results {
			if prior.resource == ref.resource && prior.version == ref.version && result.err == nil && result.material.version != material.version {
				return SourceMaterial{}, NewSourceError(SourceErrorUnavailable, s.Backend())
			}
		}
	}
	s.results[ref] = capturedSource{material, err}
	return material, err
}

type policyChainSnapshot struct {
	CloudChain
	results map[catalogs.ProviderCredentialProfileID]capturedSource
}

func (s *policyChainSnapshot) Resolve(ctx context.Context, profile catalogs.ProviderCredentialProfile, fields map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField) (SourceMaterial, error) {
	if value, exists := s.results[profile.ID]; exists {
		return value.material, value.err
	}
	material, err := s.CloudChain.Resolve(ctx, profile, fields)
	s.results[profile.ID] = capturedSource{material, err}
	return material, err
}

func validateSecretVersionScope(policies map[catalogs.ProviderCredentialFieldID]ReferencePolicy) error {
	type resourceKey struct {
		backend  ReferenceBackend
		resource string
	}
	versions := make(map[resourceKey]string)
	for _, policy := range policies {
		ref := policy.Reference
		if !isDirectSecretBackend(ref.backend) {
			continue
		}
		key := resourceKey{ref.backend, ref.resource}
		if previous, exists := versions[key]; exists && previous != ref.version {
			return &ReferenceError{Field: "version", Message: "one secret resource requires one version selection"}
		}
		versions[key] = ref.version
	}
	return nil
}
