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

func TestMonitorPropagationIncludesReadLatency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		set := authorityPair(t)
		permit := monitoredPermit(t, set)
		var sequence atomic.Uint64
		sequence.Store(1)
		owners := make([]WatchedAuthority, 0, 2)
		for _, name := range []string{"kv", "sql"} {
			owners = append(owners, WatchedAuthority{Authority: name, Reader: revisionFunc(func(context.Context) (revision.Stamp, error) {
				value := sequence.Load()
				time.Sleep(900 * time.Millisecond)
				return revision.Stamp{Epoch: name + "-epoch", Sequence: value}, nil
			})})
		}
		monitor, err := NewMonitor(owners, set, time.Second, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := monitor.Close(context.Background()); err != nil {
				t.Error(err)
			}
		}()
		monitor.Start(t.Context())
		synctest.Wait()
		sequence.Store(2)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if err := permit.Check(time.Now(), true); !errors.Is(err, ErrWithdrawn) {
			t.Fatalf("permission after propagation target = %v", err)
		}
		if err := monitor.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}
