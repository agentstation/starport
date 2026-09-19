package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAdvisoryWorkersOwnStartAndShutdown(t *testing.T) {
	var starts atomic.Int32
	entered := make(chan struct{}, 2)
	worker := func(ctx context.Context) { starts.Add(1); entered <- struct{}{}; <-ctx.Done() }
	owner := &advisoryWorkers{workers: []func(context.Context){worker, worker}}
	require.Zero(t, starts.Load(), "construction must not start workers")
	owner.Start(t.Context())
	owner.Start(t.Context())
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not start")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, owner.Close(ctx))
	require.NoError(t, owner.Close(ctx))
	owner.Start(t.Context())
	require.Equal(t, int32(2), starts.Load(), "closed workers cannot restart")
}

func TestAdvisoryWorkersCloseBeforeStart(t *testing.T) {
	owner := &advisoryWorkers{workers: []func(context.Context){func(context.Context) { t.Error("closed worker started") }}}
	require.NoError(t, owner.Close(t.Context()))
	owner.Start(t.Context())
}
