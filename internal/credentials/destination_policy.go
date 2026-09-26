package credentials

import "github.com/agentstation/starmap/pkg/catalogs"

// DestinationPolicy approves a destination contract for one provider and credential role.
// Bind creates a handle-specific grant from the actual selected material.
// Account access and credential selection remain separate requirements.
type DestinationPolicy struct {
	provider catalogs.ProviderID
	role     string
	contract *destinationContract
}

type destinationPolicyKey struct {
	provider catalogs.ProviderID
	role     string
	profile  catalogs.ProviderCredentialProfileID
}

// NewDestinationPolicy validates an approved deployment contract without network access.
// The caller must approve its use for every credential handle in the selected role.
func NewDestinationPolicy(provider catalogs.ProviderID, role string, profile catalogs.ProviderCredentialProfile, destinations []Destination) (*DestinationPolicy, error) {
	if provider == "" || role == "" {
		return nil, ErrDestinationUnapproved
	}
	contract, err := newDestinationContract(profile, destinations)
	if err != nil {
		return nil, err
	}
	return &DestinationPolicy{provider: provider, role: role, contract: contract}, nil
}

// Revoke invalidates this policy and every request grant derived from it.
func (p *DestinationPolicy) Revoke() {
	if p != nil && p.contract != nil {
		p.contract.revoked.Store(true)
	}
}
