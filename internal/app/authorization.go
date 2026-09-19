package app

import (
	"context"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/identity"
)

const (
	authorizationKV                 = "gateway-policy"
	authorizationSQL                = "identity-policy"
	authorizationRevisionInterval   = time.Second
	authorizationRevisionTimeout    = time.Second
	authorizationPermissionLifetime = 60 * time.Second
)

// authorizationOwner holds this replica's independent policy fences and revision monitor.
type authorizationOwner struct {
	kv, sql     *authorization.Fence
	kvRevision  *revision.KV
	sqlRevision *revision.SQL
	authorities *authorization.AuthoritySet
	monitor     *authorization.Monitor
	cache       *authorization.Cache
	clock       authorization.Clock
}

// openAuthorization establishes durable epochs before any policy repository opens.
// Construction initializes markers. App.Run starts the revision workers.
func (b *runtimeBuilder) openAuthorization() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	kv, sql := revision.NewKV(b.application.store, nil), revision.NewSQL(b.sqlDB, nil)
	kvStamp, err := kv.Initialize(ctx)
	if err != nil {
		return fmt.Errorf("initialize gateway policy authority: %w", err)
	}
	sqlStamp, err := sql.Initialize(ctx)
	if err != nil {
		return fmt.Errorf("initialize identity policy authority: %w", err)
	}
	owner := &authorizationOwner{
		kv: authorization.NewFence(authorizationKV, kvStamp.Epoch), sql: authorization.NewFence(authorizationSQL, sqlStamp.Epoch), kvRevision: kv, sqlRevision: sql,
	}
	owner.authorities, err = authorization.NewAuthoritySet(owner.kv, owner.sql)
	if err != nil {
		return err
	}
	for _, evidence := range []authorization.Evidence{
		{Authority: authorizationKV, Epoch: kvStamp.Epoch, Sequence: kvStamp.Sequence},
		{Authority: authorizationSQL, Epoch: sqlStamp.Epoch, Sequence: sqlStamp.Sequence},
	} {
		if err := owner.authorities.Observe(evidence); err != nil {
			return err
		}
	}
	owner.monitor, err = authorization.NewMonitor([]authorization.WatchedAuthority{{Authority: authorizationKV, Reader: kv}, {Authority: authorizationSQL, Reader: sql}}, owner.authorities, authorizationRevisionInterval, authorizationRevisionTimeout)
	if err != nil {
		return err
	}
	b.application.authorization = owner
	b.application.own("authorization revisions", owner.Close)
	return nil
}

func (o *authorizationOwner) Close(ctx context.Context) error {
	o.kv.Withdraw()
	o.sql.Withdraw()
	return o.monitor.Close(ctx)
}

// openAuthorizationCache bounds gateway policy with process-local elapsed time.
// External catalog deadlines remain owned by the catalog permission contract.
func (b *runtimeBuilder) openAuthorizationCache() error {
	owner := b.application.authorization
	owner.clock = func() (time.Time, bool) { return time.Now(), true }
	repositories := b.identityRepos
	if repositories.Teams == nil {
		var err error
		repositories, err = identity.Open(b.sqlDB, identity.WithAuthorizationFence(owner.sql.BeginMutation))
		if err != nil {
			return err
		}
	}
	source, err := authorization.NewRepositorySource(authorization.RepositorySources{
		Users: repositories.Users, Grants: repositories.AccountGrants,
		Keys: authorization.LocalKeys{Keys: b.apiKeys, Anonymous: apikey.Anonymous(b.config.Security.UnauthenticatedScopes)}, Accounts: b.accounts, Teams: repositories.Teams, KV: owner.kvRevision, SQL: owner.sqlRevision, KVAuthority: authorizationKV, SQLAuthority: authorizationSQL,
	}, owner.authorities, owner.clock, authorizationPermissionLifetime)
	if err != nil {
		return err
	}
	owner.cache, err = authorization.NewCache(source, owner.authorities, authorization.CacheLimits{
		Entries: 1024, Bytes: 16 << 20, BundleBytes: 64 << 10, ConcurrentLoads: 16, TenantLoads: 4, LoadTimeout: time.Second, PermissionLifetime: authorizationPermissionLifetime,
	}, owner.clock)
	if err != nil {
		return err
	}
	b.application.own("authorization cache", func(context.Context) error { owner.cache.Close(); return nil })
	return nil
}
