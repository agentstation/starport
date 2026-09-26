package server

import (
	"errors"
	"net/http"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/server/requestctx"
)

var errDiscoveryIdentity = errors.New("catalog discovery identity is unavailable")

// discoveryViewer reloads the authenticated identity and its governing account.
// Missing records and storage errors cannot expand disclosure permission.
func (m *AuthMiddleware) discoveryViewer(r *http.Request) (controllers.DiscoveryViewer, error) {
	if m.authorization != nil {
		bundle, err := requestctx.Authorization(r.Context())
		if err != nil {
			return controllers.DiscoveryViewer{}, errDiscoveryIdentity
		}
		key, owner := bundle.Key(), bundle.Account()
		return controllers.DiscoveryViewer{Key: key.APIKey, Account: owner.Account, KeyRevision: key.Revision, AccountRevision: owner.Revision}, nil
	}
	original, ok := requestctx.GetAPIKeyModel(r.Context())
	if !ok || original == nil || m.accounts == nil {
		return controllers.DiscoveryViewer{}, errDiscoveryIdentity
	}
	var key apikey.APIKey
	var keyRevision uint64
	switch original.ID {
	case apikey.AnonymousKeyID:
		if !m.policy.Disabled() {
			return controllers.DiscoveryViewer{}, errDiscoveryIdentity
		}
		key = m.anonymous
	case apikey.LocalOperatorKeyID:
		if _, _, ok := requestctx.GetConsoleSession(r.Context()); !ok || m.sessions == nil {
			return controllers.DiscoveryViewer{}, errDiscoveryIdentity
		}
		ctx, err := m.sessionContext(r)
		if err != nil {
			return controllers.DiscoveryViewer{}, errDiscoveryIdentity
		}
		current, ok := requestctx.GetAPIKeyModel(ctx)
		if !ok || current == nil {
			return controllers.DiscoveryViewer{}, errDiscoveryIdentity
		}
		key = *current
	default:
		if m.apiKeys == nil {
			return controllers.DiscoveryViewer{}, errDiscoveryIdentity
		}
		current, err := m.apiKeys.GetByID(r.Context(), original.ID)
		if err != nil {
			return controllers.DiscoveryViewer{}, errDiscoveryIdentity
		}
		key, keyRevision = current.APIKey, current.Revision
		if key.Hash != original.Hash {
			return controllers.DiscoveryViewer{}, errDiscoveryIdentity
		}
	}
	if key.EffectiveAccountID() != original.EffectiveAccountID() {
		return controllers.DiscoveryViewer{}, errDiscoveryIdentity
	}
	record, err := m.accounts.GetByID(r.Context(), key.EffectiveAccountID())
	if err != nil {
		return controllers.DiscoveryViewer{}, errDiscoveryIdentity
	}
	return controllers.DiscoveryViewer{Key: key, Account: record.Account, KeyRevision: keyRevision, AccountRevision: record.Revision}, nil
}
