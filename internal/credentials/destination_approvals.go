package credentials

import "github.com/agentstation/starmap/pkg/catalogs"

// DestinationApprovals holds one applied set of explicit credential grants.
// Its caller selects the authority. Catalog refresh must not construct approvals.
type DestinationApprovals struct {
	grants   map[destinationApprovalKey]*DestinationGrant
	policies map[destinationPolicyKey]*DestinationPolicy
}

type destinationApprovalKey struct {
	identity DestinationIdentity
	profile  catalogs.ProviderCredentialProfileID
}

// NewDestinationApprovals rejects ambiguous grants for the same credential profile.
// An empty set denies all credential destinations.
func NewDestinationApprovals(grants []*DestinationGrant, policies ...*DestinationPolicy) (*DestinationApprovals, error) {
	result := &DestinationApprovals{grants: make(map[destinationApprovalKey]*DestinationGrant, len(grants)), policies: make(map[destinationPolicyKey]*DestinationPolicy, len(policies))}
	for _, grant := range grants {
		if grant == nil || grant.destinationContract == nil {
			return nil, ErrDestinationUnapproved
		}
		key := destinationApprovalKey{identity: grant.identity, profile: grant.profile.ID}
		if _, exists := result.grants[key]; exists {
			return nil, ErrDestinationUnapproved
		}
		result.grants[key] = grant
	}
	for _, policy := range policies {
		if policy == nil || policy.contract == nil {
			return nil, ErrDestinationUnapproved
		}
		key := destinationPolicyKey{provider: policy.provider, role: policy.role, profile: policy.contract.profile.ID}
		if _, exists := result.policies[key]; exists {
			return nil, ErrDestinationUnapproved
		}
		result.policies[key] = policy
	}
	return result, nil
}

// Bind selects approval from the material's actual credential handle.
// It reads only the applied policy in memory.
func (a *DestinationApprovals) Bind(provider catalogs.ProviderID, role string, material Material, operation catalogs.ProviderOperation) (Material, error) {
	if a == nil || material.Handle() == "" {
		return Material{}, ErrDestinationUnapproved
	}
	identity := DestinationIdentity{Provider: provider, Role: role, Handle: material.Handle()}
	var grant DestinationGrant
	if exact := a.grants[destinationApprovalKey{identity: identity, profile: material.profile.ID}]; exact != nil {
		grant = *exact
	} else if policy := a.policies[destinationPolicyKey{provider: provider, role: role, profile: material.profile.ID}]; policy != nil {
		grant = DestinationGrant{identity: identity, destinationContract: policy.contract}
	}
	if grant.destinationContract == nil || grant.revoked.Load() || !sameDestinationProfile(grant.profile, material.profile) {
		return Material{}, ErrDestinationUnapproved
	}
	for _, target := range grant.targets {
		if target.operation == operation {
			return material.WithDestinationGrant(&grant, identity, operation), nil
		}
	}
	return Material{}, ErrDestinationUnapproved
}
