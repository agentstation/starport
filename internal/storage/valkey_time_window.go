package storage

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// AuthorityTime reads the approved primary's clock. Replica clocks do not select windows.
func (b *valkeyIncarnationStore) AuthorityTime(ctx context.Context) (time.Time, error) {
	cmd := b.store.client.B().Eval().Script(valkeyApprovedIncarnation + "return redis.call('TIME')").Numkeys(0).Arg(b.identity).Build()
	values, err := b.store.do(ctx, cmd).AsStrSlice()
	if err != nil {
		return time.Time{}, incarnationError(err)
	}
	if len(values) != 2 {
		return time.Time{}, errors.New("invalid authority time response")
	}
	seconds, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	micros, err := strconv.ParseInt(values[1], 10, 64)
	if err != nil || micros < 0 || micros >= 1_000_000 {
		return time.Time{}, errors.New("invalid authority time microseconds")
	}
	return time.Unix(seconds, micros*1000).UTC(), nil
}

// CompareAndSwapInWindow checks time and incarnation with the conditional writes.
func (b *valkeyIncarnationStore) CompareAndSwapInWindow(ctx context.Context, mutations []CompareAndSwapMutation, window TimeWindow) error {
	return b.compareAndSwap(ctx, mutations, window)
}

var _ TimeBoundStore = (*valkeyIncarnationStore)(nil)
