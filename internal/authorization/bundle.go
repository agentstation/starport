package authorization

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"slices"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/identity"
)

// Identity scopes one authenticated caller lookup. Tenant must come from trusted routing.
// An unresolved bearer lookup uses an empty tenant until its source verifies ownership.
type Identity struct {
	Tenant  string
	Subject string
}

// Candidate contains one coherent source read with explicit dependency revisions.
// A nil team confirms absence only when the key has no team dependency.
type Candidate struct {
	Key      apikey.Record
	Account  account.Record
	Team     *identity.TeamRecord
	Evidence []Evidence
}

// Bundle holds immutable caller records and their permission receipt.
type Bundle struct {
	key     apikey.Record
	account account.Record
	team    *identity.TeamRecord
	receipt Permit
	bytes   int
}

func freeze(candidate Candidate, identity Identity, receipt Permit, limit int) (*Bundle, error) {
	key := candidate.Key.APIKey
	if key.Hash != identity.Subject || candidate.Key.Revision == 0 || key.ID == "" || !key.Active {
		return nil, ErrEvidence
	}
	if candidate.Account.Revision == 0 || candidate.Account.Account.ID != key.EffectiveAccountID() || !candidate.Account.Account.Active {
		return nil, ErrEvidence
	}
	if identity.Tenant != "" && identity.Tenant != candidate.Account.Account.ID {
		return nil, ErrEvidence
	}
	if key.TeamID == "" {
		if candidate.Team != nil {
			return nil, ErrEvidence
		}
	} else if candidate.Team == nil || candidate.Team.Revision == 0 || candidate.Team.Team.ID != key.TeamID {
		return nil, ErrEvidence
	}
	// The cold path detaches every source-owned collection, including nested metadata.
	buffer := policyBuffer{limit: limit}
	if err := json.MarshalWrite(&buffer, candidate); err != nil {
		if errors.Is(err, ErrCapacity) {
			return nil, ErrCapacity
		}
		return nil, ErrEvidence
	}
	data := buffer.Bytes()
	var owned Candidate
	if err := json.Unmarshal(data, &owned); err != nil {
		return nil, ErrEvidence
	}
	return &Bundle{key: owned.Key, account: owned.Account, team: owned.Team, receipt: receipt, bytes: len(data)}, nil
}

// Permit returns the immutable validity handle for retries and cache delivery.
func (b *Bundle) Permit() Permit { return b.receipt }

// Key returns caller-owned key policy without exposing cached collections.
func (b *Bundle) Key() apikey.Record {
	value := b.key
	value.APIKey.Scopes = slices.Clone(value.APIKey.Scopes)
	value.APIKey.AllowedModels = slices.Clone(value.APIKey.AllowedModels)
	value.APIKey.Limits = value.APIKey.Limits.Clone()
	value.APIKey.Metadata = cloneMetadata(value.APIKey.Metadata)
	if value.APIKey.ExpiresAt != nil {
		expires := *value.APIKey.ExpiresAt
		value.APIKey.ExpiresAt = &expires
	}
	return value
}

// Account returns caller-owned account policy without exposing cached collections.
func (b *Bundle) Account() account.Record {
	value := b.account
	value.Account.Limits = value.Account.Limits.Clone()
	value.Account.Metadata = cloneMetadata(value.Account.Metadata)
	if policy := value.Account.BYOKPolicy; policy != nil {
		owned := *policy
		owned.Providers = slices.Clone(policy.Providers)
		value.Account.BYOKPolicy = &owned
	}
	value.Account.Access = slices.Clone(value.Account.Access)
	for i := range value.Account.Access {
		value.Account.Access[i].Models = slices.Clone(value.Account.Access[i].Models)
	}
	return value
}

// Team returns a caller-owned team record. Absence means the key has no team.
func (b *Bundle) Team() *identity.TeamRecord {
	if b.team == nil {
		return nil
	}
	value := *b.team
	value.Team.Budget = value.Team.Budget.Clone()
	return &value
}

func cloneMetadata(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = cloneMetadataValue(value)
	}
	return result
}

func cloneMetadataValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMetadata(typed)
	case []any:
		owned := make([]any, len(typed))
		for i, item := range typed {
			owned[i] = cloneMetadataValue(item)
		}
		return owned
	default:
		return value
	}
}

type policyBuffer struct {
	bytes.Buffer
	limit int
}

func (b *policyBuffer) Write(value []byte) (int, error) {
	if len(value) > b.limit-b.Len() {
		return 0, ErrCapacity
	}
	return b.Buffer.Write(value)
}
