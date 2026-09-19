package credentials

import "github.com/agentstation/starmap/pkg/catalogs"

// DestinationApprovals holds one applied set of explicit credential grants.
// Its caller selects the authority. Catalog refresh must not construct approvals.
type DestinationApprovals struct {
	grants map[destinationApprovalKey]*DestinationGrant
}

type destinationApprovalKey struct {
	identity DestinationIdentity
	profile  catalogs.ProviderCredentialProfileID
}

// NewDestinationApprovals rejects ambiguous grants for the same credential profile.
// An empty set denies all credential destinations.
func NewDestinationApprovals(grants []*DestinationGrant) (*DestinationApprovals, error) {
	result := &DestinationApprovals{grants: make(map[destinationApprovalKey]*DestinationGrant, len(grants))}
	for _, grant := range grants {
		if grant == nil {
			return nil, ErrDestinationUnapproved
		}
		key := destinationApprovalKey{identity: grant.identity, profile: grant.profile.ID}
		if _, exists := result.grants[key]; exists {
			return nil, ErrDestinationUnapproved
		}
		result.grants[key] = grant
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
	grant := a.grants[destinationApprovalKey{identity: identity, profile: material.profile.ID}]
	if grant == nil || grant.revoked.Load() || !sameDestinationProfile(grant.profile, material.profile) {
		return Material{}, ErrDestinationUnapproved
	}
	for _, target := range grant.targets {
		if target.operation == operation {
			return material.WithDestinationGrant(grant, identity, operation), nil
		}
	}
	return Material{}, ErrDestinationUnapproved
}
