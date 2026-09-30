package recovery

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

var errLostLocalCommitReply = errors.New("lost SQLite completion reply")

type localLostCommitDriver struct{ lost atomic.Bool }

func (d *localLostCommitDriver) Open(name string) (driver.Conn, error) {
	native, err := (&sqlite.Driver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &localLostCommitConnection{Conn: native, owner: d}, nil
}

type localLostCommitConnection struct {
	driver.Conn
	owner *localLostCommitDriver
}

func (c *localLostCommitConnection) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	native, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	result, err := native.ExecContext(ctx, query, args)
	if err == nil && strings.EqualFold(strings.TrimSpace(query), "COMMIT") && c.owner.lost.CompareAndSwap(false, true) {
		return nil, errLostLocalCommitReply
	}
	return result, err
}
func (c *localLostCommitConnection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	native, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return native.QueryContext(ctx, query, args)
}

func TestImportedLocalCompletionLostSQLReplyRetainsNativeCommit(t *testing.T) {
	witness, cfg, target, request, position, _ := importedLocalCompletionFixture(t)
	ctx := localCompletionContext(t)
	original := witness.db.DB
	hook := &localLostCommitDriver{}
	name := "local-completion-lost-" + rand.Text()
	sql.Register(name, hook)
	hooked, err := sql.Open(name, cfg.SQLite.Path)
	require.NoError(t, err)
	hooked.SetMaxOpenConns(1)
	witness.db.DB = hooked
	t.Cleanup(func() { require.NoError(t, hooked.Close()); require.NoError(t, original.Close()) })
	_, err = witness.CompleteImportedLocalAt(ctx, target, request, position)
	require.ErrorIs(t, err, errLostLocalCommitReply)
	require.True(t, hook.lost.Load())
	require.NoError(t, witness.db.CheckImportBarrier(ctx), "a lost reply does not prove rollback")
	require.NoError(t, witness.db.CheckActivatedRelationalImportAt(ctx, request.Snapshot, request.Import, position, request.DecisionSHA256))
	current, err := witness.Current(ctx, request.Closed.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, importedLocalCompletedRecord(request), current)
	people, err := identity.Open(witness.db)
	require.NoError(t, err)
	_, err = people.Users.Create(ctx, identity.User{ID: "after-uncertain-ack", Subject: "preserved-private-subject"})
	require.NoError(t, err)
	require.NoError(t, hooked.Close())
	reopened, err := sqlstore.Open(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	restarted, err := New(reopened)
	require.NoError(t, err)
	again, err := restarted.CompleteImportedLocalAt(ctx, target, request, position)
	require.NoError(t, err)
	require.Equal(t, current, again)
	people, err = identity.Open(reopened)
	require.NoError(t, err)
	retained, err := people.Users.GetByID(ctx, "after-uncertain-ack")
	require.NoError(t, err)
	require.Equal(t, "preserved-private-subject", retained.User.Subject)
	withdrawn, err := restarted.Close(ctx, current)
	require.NoError(t, err)
	_, err = restarted.CompleteImportedLocalAt(ctx, target, request, position)
	require.ErrorIs(t, err, ErrConflict)
	actual, err := restarted.Current(ctx, current.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, withdrawn, actual)
	require.NoError(t, reopened.CheckActivatedRelationalImportAt(ctx, request.Snapshot, request.Import, position, request.DecisionSHA256))
}
