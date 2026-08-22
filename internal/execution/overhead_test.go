package execution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOverheadTimerExcludesUpstreamWait(t *testing.T) {
	ctx, timer := WithOverheadTimer(context.Background())
	require.Same(t, timer, OverheadTimerFrom(ctx))

	endUpstream := timer.TrackUpstream()
	time.Sleep(120 * time.Millisecond)
	endUpstream()

	overhead := timer.OverheadMS()
	require.Less(t, overhead, int64(60), "upstream wait must not count as overhead")
}

func TestOverheadTimerAccumulatesSequentialWaits(t *testing.T) {
	_, timer := WithOverheadTimer(context.Background())

	for range 2 {
		endUpstream := timer.TrackUpstream()
		time.Sleep(60 * time.Millisecond)
		endUpstream()
	}

	require.Less(t, timer.OverheadMS(), int64(60))
}

func TestOverheadTimerNilSafe(t *testing.T) {
	var timer *OverheadTimer
	require.NotPanics(t, func() {
		endUpstream := timer.TrackUpstream()
		endUpstream()
	})
	require.Zero(t, timer.OverheadMS())
}

func TestOverheadTimerFromMissingContext(t *testing.T) {
	require.Nil(t, OverheadTimerFrom(context.Background()))
}
