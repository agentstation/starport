package authorization

import (
	"context"
	"errors"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/identity"
)

const (
	// AnonymousSubject identifies host-selected anonymous policy, never a bearer hash.
	AnonymousSubject = "local:anonymous"
	// OperatorSubject identifies verified machine-local operator policy.
	OperatorSubject = "local:operator"
)

// LocalKeys resolves host policy without storing synthetic credentials.
// Only authentication middleware can select these non-hash subjects.
type LocalKeys struct {
	Keys      KeyReader
	Anonymous apikey.APIKey
}

// GetByHash delegates bearer hashes and supplies the host's fixed local identities.
func (s LocalKeys) GetByHash(ctx context.Context, hash string) (apikey.Record, error) {
	var key apikey.APIKey
	switch hash {
	case AnonymousSubject:
		key = s.Anonymous
	case OperatorSubject:
		key = apikey.LocalOperator()
	default:
		return s.Keys.GetByHash(ctx, hash)
	}
	key.Hash = hash
	return apikey.Record{Revision: 1, APIKey: key}, nil
}

// RevisionReader reads the current marker from the authoritative store.
// Replicas and caches without a linearizable read contract cannot supply it.
type RevisionReader interface {
	Read(context.Context) (revision.Stamp, error)
}

// KeyReader resolves the durable key policy by its authentication hash.
type KeyReader interface {
	GetByHash(context.Context, string) (apikey.Record, error)
}

// AccountReader reads the account policy that a key names.
type AccountReader interface {
	GetByID(context.Context, string) (account.Record, error)
}

// TeamReader reads a required team and its budget policy.
type TeamReader interface {
	GetByID(context.Context, string) (identity.TeamRecord, error)
}

// RepositorySource checks independent revision markers around one policy read.
// The constructor starts without I/O. Startup initializes markers and creates fences.
type RepositorySource struct {
	keys                      KeyReader
	accounts                  AccountReader
	teams                     TeamReader
	kv, sql                   RevisionReader
	authorities               *AuthoritySet
	clock                     Clock
	lifetime                  time.Duration
	kvAuthority, sqlAuthority string
}

// RepositorySources binds policy records to their authoritative revision owners.
type RepositorySources struct {
	Keys                      KeyReader
	Accounts                  AccountReader
	Teams                     TeamReader
	KV, SQL                   RevisionReader
	KVAuthority, SQLAuthority string
}

// NewRepositorySource requires exactly the configured KV and SQL authorities.
func NewRepositorySource(s RepositorySources, authorities *AuthoritySet, clock Clock, lifetime time.Duration) (*RepositorySource, error) {
	if s.Keys == nil || s.Accounts == nil || s.Teams == nil || s.KV == nil || s.SQL == nil || authorities == nil || clock == nil || lifetime <= 0 || s.KVAuthority == "" || s.SQLAuthority == "" || s.KVAuthority == s.SQLAuthority || len(authorities.fences) != 2 {
		return nil, ErrEvidence
	}
	for _, fence := range authorities.fences {
		if fence.authority != s.KVAuthority && fence.authority != s.SQLAuthority {
			return nil, ErrEvidence
		}
	}
	return &RepositorySource{keys: s.Keys, accounts: s.Accounts, teams: s.Teams, kv: s.KV, sql: s.SQL, authorities: authorities, clock: clock, lifetime: lifetime, kvAuthority: s.KVAuthority, sqlAuthority: s.SQLAuthority}, nil
}

// Load refuses records that span a policy change. The caller can retry the whole load.
// Observed requirements revoke old permits even when replacement records cannot load.
func (s *RepositorySource) Load(ctx context.Context, caller Identity) (Candidate, error) {
	if err := ctx.Err(); err != nil {
		return Candidate{}, err
	}
	started, healthy := s.clock()
	if !healthy {
		return Candidate{}, ErrUnavailable
	}
	beforeKV, kvErr := s.observe(ctx, s.kv, s.kvAuthority)
	beforeSQL, sqlErr := s.observe(ctx, s.sql, s.sqlAuthority)
	if err := errors.Join(kvErr, sqlErr); err != nil {
		return Candidate{}, err
	}
	candidate, recordErr := s.records(ctx, caller)
	// Reverse order creates a common interval for both independent authorities.
	afterSQL, sqlErr := s.observe(ctx, s.sql, s.sqlAuthority)
	afterKV, kvErr := s.observe(ctx, s.kv, s.kvAuthority)
	if err := errors.Join(sqlErr, kvErr, ctx.Err()); err != nil {
		return Candidate{}, err
	}
	if beforeKV != afterKV || beforeSQL != afterSQL {
		return Candidate{}, ErrWithdrawn
	}
	if recordErr != nil {
		return Candidate{}, recordErr
	}
	finished, healthy := s.clock()
	if !healthy || finished.Before(started) {
		return Candidate{}, ErrUnavailable
	}
	candidate.Evidence = []Evidence{
		{Authority: s.kvAuthority, Epoch: beforeKV.Epoch, Sequence: beforeKV.Sequence, VerifiedAt: started, ValidUntil: started.Add(s.lifetime)},
		{Authority: s.sqlAuthority, Epoch: beforeSQL.Epoch, Sequence: beforeSQL.Sequence, VerifiedAt: started, ValidUntil: started.Add(s.lifetime)},
	}
	return candidate, nil
}

func (s *RepositorySource) observe(ctx context.Context, reader RevisionReader, authority string) (revision.Stamp, error) {
	stamp, err := reader.Read(ctx)
	if err != nil {
		return revision.Stamp{}, errors.Join(ErrUnavailable, err)
	}
	if stamp.Epoch == "" || len(stamp.Epoch) > 256 || stamp.Sequence == 0 {
		return revision.Stamp{}, ErrEvidence
	}
	if err := s.authorities.Observe(Evidence{Authority: authority, Epoch: stamp.Epoch, Sequence: stamp.Sequence}); err != nil {
		return revision.Stamp{}, err
	}
	return stamp, nil
}

func (s *RepositorySource) records(ctx context.Context, caller Identity) (Candidate, error) {
	key, err := s.keys.GetByHash(ctx, caller.Subject)
	if err != nil {
		return Candidate{}, err
	}
	if key.Revision == 0 || key.APIKey.Hash != caller.Subject || key.APIKey.ID == "" {
		return Candidate{}, ErrEvidence
	}
	owner, err := s.accounts.GetByID(ctx, key.APIKey.EffectiveAccountID())
	if err != nil {
		return Candidate{}, err
	}
	if owner.Revision == 0 || owner.Account.ID != key.APIKey.EffectiveAccountID() || (caller.Tenant != "" && caller.Tenant != owner.Account.ID) {
		return Candidate{}, ErrEvidence
	}
	result := Candidate{Key: key, Account: owner}
	if key.APIKey.TeamID != "" {
		team, err := s.teams.GetByID(ctx, key.APIKey.TeamID)
		if err != nil {
			return Candidate{}, err
		}
		if team.Revision == 0 || team.Team.ID != key.APIKey.TeamID {
			return Candidate{}, ErrEvidence
		}
		result.Team = &team
	}
	return result, nil
}
