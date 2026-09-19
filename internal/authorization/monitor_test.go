package authorization

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/agentstation/starport/internal/authorization/revision"
)

func monitoredPermit(t *testing.T, set *AuthoritySet) Permit {
	t.Helper()
	tickets, err := set.start()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	permit, err := tickets.accept([]Evidence{authorityEvidence("kv", "kv-epoch", 1, now), authorityEvidence("sql", "sql-epoch", 1, now)}, now, 5*time.Minute, 30*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	return permit
}

func TestMonitorCatchesMissedChangeDuringOtherAuthorityOutage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		set := authorityPair(t)
		permit := monitoredPermit(t, set)
		var sqlSequence atomic.Uint64
		sqlSequence.Store(1)
		var blockKV atomic.Bool
		var kvCalls atomic.Int64
		owners := []WatchedAuthority{
			{Authority: "kv", Reader: revisionFunc(func(ctx context.Context) (revision.Stamp, error) {
				kvCalls.Add(1)
				if blockKV.Load() {
					<-ctx.Done()
					return revision.Stamp{}, ctx.Err()
				}
				return revision.Stamp{Epoch: "kv-epoch", Sequence: 1}, nil
			})},
			{Authority: "sql", Reader: revisionFunc(func(context.Context) (revision.Stamp, error) {
				return revision.Stamp{Epoch: "sql-epoch", Sequence: sqlSequence.Load()}, nil
			})},
		}
		monitor, err := NewMonitor(owners, set, time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if kvCalls.Load() != 0 {
			t.Fatal("constructor read storage")
		}
		monitor.Start(t.Context())
		monitor.Start(t.Context())
		synctest.Wait()
		if kvCalls.Load() != 1 {
			t.Fatal("duplicate worker")
		}
		blockKV.Store(true)
		sqlSequence.Store(2)
		time.Sleep(time.Second)
		synctest.Wait()
		if err := permit.Check(time.Now(), true); !errors.Is(err, ErrWithdrawn) {
			t.Fatalf("missed change retained permission: %v", err)
		}
		statuses := monitor.Status()
		if statuses[1].Sequence != 2 || statuses[1].Failure != "" {
			t.Fatalf("SQL status = %+v", statuses[1])
		}
		statuses[1].Sequence = 999
		if monitor.Status()[1].Sequence != 2 {
			t.Fatal("caller changed monitor state")
		}
		if err := monitor.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		calls := kvCalls.Load()
		monitor.Start(t.Context())
		synctest.Wait()
		if kvCalls.Load() != calls {
			t.Fatal("closed monitor restarted")
		}
	})
}

func TestMonitorObservationsDoNotRenewPermission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		set := authorityPair(t)
		permit := monitoredPermit(t, set)
		var offline atomic.Bool
		owners := make([]WatchedAuthority, 0, 2)
		for _, name := range []string{"kv", "sql"} {
			owners = append(owners, WatchedAuthority{Authority: name, Reader: revisionFunc(func(context.Context) (revision.Stamp, error) {
				if offline.Load() {
					return revision.Stamp{}, errors.New("secret connection details")
				}
				return revision.Stamp{Epoch: name + "-epoch", Sequence: 1}, nil
			})})
		}
		monitor, err := NewMonitor(owners, set, time.Second, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		monitor.Start(t.Context())
		synctest.Wait()
		original := permit.Deadline()
		offline.Store(true)
		time.Sleep(time.Second)
		synctest.Wait()
		if err := permit.Check(time.Now(), true); err != nil {
			t.Fatal("transient outage revoked still-valid retained receipt", err)
		}
		for _, status := range monitor.Status() {
			if status.Failure != "unavailable" {
				t.Fatalf("unsafe failure status = %q", status.Failure)
			}
		}
		offline.Store(false)
		time.Sleep(269 * time.Second)
		synctest.Wait()
		if permit.Deadline() != original {
			t.Fatal("monitor extended receipt")
		}
		if err := permit.Check(time.Now(), true); !errors.Is(err, ErrExpired) {
			t.Fatalf("expired receipt = %v", err)
		}
		if err := monitor.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMonitorEpochChangeCannotRecoverUnderOldEpoch(t *testing.T) {
	set := authorityPair(t)
	permit := monitoredPermit(t, set)
	epoch := "new-epoch"
	owners := []WatchedAuthority{
		{Authority: "kv", Reader: revisionFunc(func(context.Context) (revision.Stamp, error) { return revision.Stamp{Epoch: epoch, Sequence: 1}, nil })},
		{Authority: "sql", Reader: revisionFunc(func(context.Context) (revision.Stamp, error) {
			return revision.Stamp{Epoch: "sql-epoch", Sequence: 1}, nil
		})},
	}
	monitor, err := NewMonitor(owners, set, time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	monitor.check(t.Context(), 0)
	if err := permit.Check(time.Now(), true); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("new epoch = %v", err)
	}
	epoch = "kv-epoch"
	monitor.check(t.Context(), 0)
	if monitor.Status()[0].Failure != "epoch_changed" {
		t.Fatal("old epoch reported recovery without a new fence")
	}
}

func TestMonitorRejectsRevisionRollback(t *testing.T) {
	set := authorityPair(t)
	if err := set.Observe(Evidence{Authority: "sql", Epoch: "sql-epoch", Sequence: 2}); err != nil {
		t.Fatal(err)
	}
	owners := []WatchedAuthority{
		{Authority: "kv", Reader: revisionFunc(func(context.Context) (revision.Stamp, error) {
			return revision.Stamp{Epoch: "kv-epoch", Sequence: 1}, nil
		})},
		{Authority: "sql", Reader: revisionFunc(func(context.Context) (revision.Stamp, error) {
			return revision.Stamp{Epoch: "sql-epoch", Sequence: 1}, nil
		})},
	}
	monitor, err := NewMonitor(owners, set, time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	monitor.check(t.Context(), 1)
	if monitor.Status()[1].Failure != "invalid_revision" {
		t.Fatal("rollback reported healthy")
	}
}

func TestMonitorCloseBeforeStart(t *testing.T) {
	set := authorityPair(t)
	owners := make([]WatchedAuthority, 0, 2)
	for _, name := range []string{"kv", "sql"} {
		owners = append(owners, WatchedAuthority{Authority: name, Reader: revisionFunc(func(context.Context) (revision.Stamp, error) {
			t.Error("closed monitor read storage")
			return revision.Stamp{}, ErrUnavailable
		})})
	}
	monitor, err := NewMonitor(owners, set, time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	monitor.Start(t.Context())
}

func TestMonitorRetainsKnownWithdrawalAfterReadDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		set := authorityPair(t)
		permit := monitoredPermit(t, set)
		owners := []WatchedAuthority{
			{Authority: "kv", Reader: revisionFunc(func(ctx context.Context) (revision.Stamp, error) {
				<-ctx.Done()
				return revision.Stamp{Epoch: "kv-epoch", Sequence: 2}, nil
			})},
			{Authority: "sql", Reader: revisionFunc(func(context.Context) (revision.Stamp, error) {
				return revision.Stamp{Epoch: "sql-epoch", Sequence: 1}, nil
			})},
		}
		monitor, err := NewMonitor(owners, set, time.Second, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		monitor.Start(t.Context())
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		if err := permit.Check(time.Now(), true); !errors.Is(err, ErrWithdrawn) {
			t.Fatalf("known late withdrawal ignored: %v", err)
		}
		if monitor.Status()[0].Failure != "deadline" {
			t.Fatal("late observation reported timely success")
		}
		if err := monitor.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}
