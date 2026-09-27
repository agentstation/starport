package reservation

import (
	"context"
	"encoding/json/v2"
	"errors"

	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/storage"
)

type holderIdentity struct {
	Scope limits.Scope `json:"scope"`
	ID    string       `json:"id"`
}

// HolderCreationMutations belong in the holder repository's atomic create batch.
// A retained identity prevents deletion and recreation from resetting usage.
// Recreated holders and newly added budgets require explicit history reconciliation.
func HolderCreationMutations(ctx context.Context, store storage.KVStore, scope limits.Scope, id string, policy *limits.Limits) ([]storage.CompareAndSwapMutation, error) {
	if store == nil || !validID(id) || (scope != limits.ScopeAccount && scope != limits.ScopeKey && scope != limits.ScopeTeam) {
		return nil, ErrInvalid
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	identity := holderIdentity{Scope: scope, ID: id}
	key := storageKey("holder", identity)
	prior, err := store.GetBounded(ctx, key, 1024)
	fresh := errors.Is(err, storage.ErrNotFound)
	if err != nil && !fresh {
		return nil, err
	}
	if !fresh {
		var stored holderIdentity
		if json.Unmarshal(prior, &stored) != nil || stored != identity {
			return nil, ErrUnavailable
		}
	}
	marker, err := encodeMutation(key, prior, identity)
	if err != nil {
		return nil, err
	}
	mutations := []storage.CompareAndSwapMutation{marker}
	if !fresh || policy == nil {
		return mutations, nil
	}
	for _, entry := range []struct {
		dimension limits.Dimension
		budget    *limits.Budget
	}{{limits.DimensionSpend, policy.Spend}, {limits.DimensionTokens, policy.Tokens}} {
		if entry.budget == nil {
			continue
		}
		mutation, err := FreshHistoryMutation(Meter{Scope: scope, Holder: id, Dimension: entry.dimension, Interval: entry.budget.Interval}, entry.budget.HistoryID)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, mutation)
	}
	return mutations, nil
}
