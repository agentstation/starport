package storage

import (
	"context"
	"time"

	"github.com/valkey-io/valkey-go"
)

func (v *ValkeyStore) commandContext(ctx context.Context) (context.Context, context.CancelFunc) {
	limit := v.operationTimeout
	if limit <= 0 {
		limit = 3 * time.Second
	}
	return context.WithTimeout(ctx, limit)
}

func (v *ValkeyStore) do(ctx context.Context, command valkey.Completed) valkey.ValkeyResult {
	ctx, cancel := v.commandContext(ctx)
	defer cancel()
	return v.client.Do(ctx, command)
}
