package app

import (
	"context"
	"fmt"
	"time"

	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/authorization/revision"
)

const (
	authorizationKV               = "gateway-policy"
	authorizationSQL              = "identity-policy"
	authorizationRevisionInterval = time.Second
	authorizationRevisionTimeout  = time.Second
)

// authorizationOwner holds this replica's independent policy fences and revision monitor.
type authorizationOwner struct {
	kv, sql     *authorization.Fence
	kvRevision  *revision.KV
	sqlRevision *revision.SQL
	authorities *authorization.AuthoritySet
	monitor     *authorization.Monitor
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
