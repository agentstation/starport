package server

import (
	"context"
	"encoding/json/v2"
	"strings"

	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/server/requestctx"
)

type retainedBatchCaller struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	Subject string `json:"subject,omitempty"`
	Receipt string `json:"receipt,omitempty"`
	Account string `json:"account"`
	Batch   string `json:"batch"`
}

func captureBatchCaller(ctx context.Context, owner string, caller retainedBatchCaller) context.Context {
	return requestctx.WithBatchAuthorization(ctx, func(account, id string) ([]byte, error) {
		if account != owner || id == "" {
			return nil, authorization.ErrDenied
		}
		owned := caller
		owned.Account, owned.Batch, owned.Version = account, id, 1
		return json.Marshal(owned)
	})
}

// BatchRecoveryRunner reconstructs current authorization from batch-scoped evidence.
// It never reads a stored bearer secret or a reusable console cookie.
func (s *Server) BatchRecoveryRunner(ctx context.Context, batch jobs.Batch) (jobs.LineRunner, error) {
	current, err := s.auth.resumeBatchCaller(ctx, batch)
	if err != nil {
		return nil, err
	}
	bundle, err := requestctx.Authorization(current)
	if err != nil {
		return nil, err
	}
	key := bundle.Key().APIKey
	if key.ID != batch.KeyID || bundle.Account().Account.ID != batch.Account || !key.HasScope("batches:write") {
		return nil, authorization.ErrDenied
	}
	admission := controllers.BatchAdmission{AccountID: batch.Account, KeyID: batch.KeyID, Reauthorize: func(ctx context.Context) (context.Context, error) { return s.auth.resumeBatchCaller(ctx, batch) }}
	return controllers.NewBatchRecoveryRunner(s.service, s.batchGovernor(), batch, admission), nil
}

func (m *AuthMiddleware) resumeBatchCaller(ctx context.Context, batch jobs.Batch) (context.Context, error) {
	var caller retainedBatchCaller
	if m.authorization == nil || m.permissionClock == nil || len(batch.Authorization) == 0 || len(batch.Authorization) > 8192 || json.Unmarshal(batch.Authorization, &caller) != nil || caller.Version != 1 || caller.Account != batch.Account || caller.Batch != batch.ID {
		return nil, authorization.ErrUnavailable
	}
	switch caller.Kind {
	case "bearer":
		if caller.Subject == "" || strings.HasPrefix(caller.Subject, "local:") || strings.HasPrefix(caller.Subject, authorization.SessionSubjectPrefix) || caller.Receipt != "" {
			return nil, authorization.ErrUnavailable
		}
		return m.cachedBearer(ctx, "", caller.Subject)
	case "anonymous":
		if caller.Subject != "" || caller.Receipt != "" {
			return nil, authorization.ErrUnavailable
		}
		return m.anonymousContext(ctx)
	case "session":
		if caller.Subject != "" || caller.Receipt == "" {
			return nil, authorization.ErrUnavailable
		}
		now, healthy := m.permissionClock()
		if !healthy {
			return nil, authorization.ErrUnavailable
		}
		session, err := m.sessions.VerifyBatchSession(caller.Receipt, batch.Account, batch.ID, now)
		if err != nil {
			return nil, err
		}
		identity := authorization.Identity{Subject: authorization.OperatorSubject, Tenant: batch.Account}
		if session.Grant == localauth.GrantIdentity {
			identity.Subject = authorization.SessionSubjectPrefix + session.Subject
		}
		ctx = requestctx.WithConsoleSession(ctx, string(session.Grant), session.Subject)
		return m.cachedPolicy(ctx, identity, func() error {
			now, healthy := m.permissionClock()
			if !healthy {
				return authorization.ErrUnavailable
			}
			if err := m.authorization.CheckDeadline(session.ExpiresAt); err != nil {
				return err
			}
			_, err := m.sessions.VerifyBatchSession(caller.Receipt, batch.Account, batch.ID, now)
			return err
		})
	default:
		return nil, authorization.ErrUnavailable
	}
}

func encodeBatchSession(account, id, receipt string) ([]byte, error) {
	return json.Marshal(retainedBatchCaller{Version: 1, Kind: "session", Account: account, Batch: id, Receipt: receipt})
}
