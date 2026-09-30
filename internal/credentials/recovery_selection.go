package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"slices"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

type recoveryDestination struct {
	Operation catalogs.ProviderOperation `json:"operation"`
	Method    string                     `json:"method"`
	Scheme    string                     `json:"scheme"`
	Host      string                     `json:"host"`
	Port      string                     `json:"port"`
	Path      string                     `json:"path"`
	Query     string                     `json:"query"`
	Template  string                     `json:"template"`
}
type recoveryDestinationContract struct {
	Profile catalogs.ProviderCredentialProfile `json:"profile"`
	Targets []recoveryDestination              `json:"targets"`
	Revoked bool                               `json:"revoked"`
}

func recoveryContract(contract *destinationContract) *recoveryDestinationContract {
	if contract == nil {
		return nil
	}
	result := &recoveryDestinationContract{Profile: contract.profile, Revoked: contract.revoked.Load()}
	for _, target := range contract.targets {
		template := ""
		if target.template != nil {
			template = target.template.String()
		}
		result.Targets = append(result.Targets, recoveryDestination{target.operation, target.method, target.scheme, target.host, target.port, target.path, target.query, template})
	}
	return result
}

// RecoverySelectionSHA256 binds private material without exporting credential values.
// The digest does not establish credential validity or authorize a destination.
func (m Material) RecoverySelectionSHA256() (string, error) {
	type validityState struct {
		Deadline time.Time `json:"deadline"`
		Revoked  bool      `json:"revoked"`
	}
	var validity *validityState
	if m.validity != nil {
		validity = &validityState{Deadline: m.validity.deadline, Revoked: m.validity.revoked == nil || m.validity.revoked.Load()}
	}
	return recoverySelectionDigest(struct {
		Profile          catalogs.ProviderCredentialProfile            `json:"profile"`
		Values           map[catalogs.ProviderCredentialFieldID]string `json:"values"`
		Metadata         MaterialMetadata                              `json:"metadata"`
		Validity         *validityState                                `json:"validity"`
		DestinationBound bool                                          `json:"destination_bound"`
		Identity         DestinationIdentity                           `json:"destination_identity"`
		Operation        catalogs.ProviderOperation                    `json:"destination_operation"`
		Contract         *recoveryDestinationContract                  `json:"destination_contract"`
	}{m.profile, m.values, m.metadata, validity, m.destinationBound, m.destination.identity, m.destination.operation, recoveryContract(m.destination.grant.destinationContract)})
}

// RecoverySelectionSHA256 binds applied grants, policies, and current revocation states.
// Nil retains the distinct installation-default selection.
func (a *DestinationApprovals) RecoverySelectionSHA256() (string, error) {
	if a == nil {
		return recoverySelectionDigest("installation-defaults")
	}
	selections := []string{}
	for _, grant := range a.grants {
		if grant == nil || grant.destinationContract == nil {
			return "", ErrDestinationUnapproved
		}
		body, err := json.Marshal(struct {
			Identity DestinationIdentity          `json:"identity"`
			Contract *recoveryDestinationContract `json:"contract"`
		}{grant.identity, recoveryContract(grant.destinationContract)}, json.Deterministic(true))
		if err != nil {
			return "", err
		}
		selections = append(selections, "grant:"+string(body))
	}
	for _, policy := range a.policies {
		if policy == nil || policy.contract == nil {
			return "", ErrDestinationUnapproved
		}
		body, err := json.Marshal(struct {
			Provider catalogs.ProviderID          `json:"provider"`
			Role     string                       `json:"role"`
			Contract *recoveryDestinationContract `json:"contract"`
		}{policy.provider, policy.role, recoveryContract(policy.contract)}, json.Deterministic(true))
		if err != nil {
			return "", err
		}
		selections = append(selections, "policy:"+string(body))
	}
	slices.Sort(selections)
	return recoverySelectionDigest(selections)
}
func recoverySelectionDigest(value any) (string, error) {
	body, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

// RecoveryMaterialSourceSHA256 binds built-in source selections without resolving credentials.
// Custom sources require a separate owner recovery contract.
func RecoveryMaterialSourceSHA256(source MaterialSource) (string, error) {
	var handle *ProviderHandle
	kind := ""
	switch selected := source.(type) {
	case nil:
		return recoverySelectionDigest("not-selected")
	case *ProviderHandle:
		handle = selected
		kind = "provider-handle"
	case cachedProviderSource:
		handle = selected.handle
		kind = "cached-provider"
	default:
		return "", ErrProviderContractRequired
	}
	if handle == nil || handle.resolver == nil {
		return "", ErrResolverRequired
	}
	type referenceState struct {
		Backend         ReferenceBackend `json:"backend"`
		Resource        string           `json:"resource"`
		Field           string           `json:"field"`
		Version         string           `json:"version"`
		FallbackAmbient bool             `json:"fallback_ambient"`
	}
	references := make(map[catalogs.ProviderCredentialFieldID]referenceState, len(handle.policies))
	for field, selected := range handle.policies {
		references[field] = referenceState{selected.Reference.backend, selected.Reference.resource, selected.Reference.field, selected.Reference.version, selected.FallbackAmbient}
	}
	return recoverySelectionDigest(struct {
		Kind              string                                                `json:"kind"`
		Provider          catalogs.ProviderID                                   `json:"provider"`
		Contract          *catalogs.ProviderCredentials                         `json:"contract"`
		References        map[catalogs.ProviderCredentialFieldID]referenceState `json:"references"`
		Forced            bool                                                  `json:"forced"`
		EnvironmentPolicy EnvironmentPolicy                                     `json:"environment_policy"`
		StarmapFallback   bool                                                  `json:"starmap_fallback"`
	}{kind, handle.provider.ID, handle.provider.Credentials, references, handle.forced, handle.resolver.environmentPolicy, handle.resolver.starmapFallback})
}
