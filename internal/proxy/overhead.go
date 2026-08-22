package proxy

import (
	"context"

	"github.com/agentstation/starport/internal/execution"
)

// OverheadHeader carries the gateway-added latency of one proxied
// response in whole milliseconds. The value excludes upstream provider
// time; on a stream it covers the work before the first byte reaches
// the client.
const OverheadHeader = "x-starport-overhead-ms"

// StartOverhead begins overhead measurement for one request. The HTTP
// layer calls it once before dispatch; attempt callbacks mark upstream
// waits on the same timer through the context.
func StartOverhead(ctx context.Context) context.Context {
	ctx, _ = execution.WithOverheadTimer(ctx)
	return ctx
}

// OverheadMS reports the gateway-added milliseconds measured so far.
// The second result is false when the request never started a timer.
func OverheadMS(ctx context.Context) (int64, bool) {
	timer := execution.OverheadTimerFrom(ctx)
	if timer == nil {
		return 0, false
	}
	return timer.OverheadMS(), true
}
