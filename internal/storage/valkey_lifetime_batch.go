package storage

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/valkey-io/valkey-go"
)

// ReadBatchWithLifetime checks incarnation and reads the batch in one native command.
func (b *valkeyIncarnationStore) ReadBatchWithLifetime(ctx context.Context, keys []string, maxBytes int) ([]LifetimeValue, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateLifetimeBatch(keys, maxBytes); err != nil {
		return nil, err
	}
	const script = valkeyApprovedIncarnation + `
local results = {}
for i, key in ipairs(KEYS) do
  if redis.call('EXISTS', key) == 0 then
    results[i] = false
  else
    if redis.call('STRLEN', key) > tonumber(ARGV[2]) then return redis.error_reply('STARPORT_VALUE_TOO_LARGE') end
    results[i] = {redis.call('GET', key), redis.call('PTTL', key)}
  end
end
return results`
	nativeKeys := make([]string, len(keys))
	for i, key := range keys {
		nativeKeys[i] = b.store.prefix + key
	}
	command := b.store.client.B().Eval().Script(script).Numkeys(int64(len(keys))).Key(nativeKeys...).Arg(b.identity, strconv.Itoa(maxBytes)).Build()
	rows, err := b.store.do(ctx, command).ToArray()
	if err != nil {
		if strings.Contains(err.Error(), "STARPORT_VALUE_TOO_LARGE") {
			return nil, ErrValueTooLarge
		}
		return nil, incarnationError(err)
	}
	if len(rows) != len(keys) {
		return nil, errors.New("invalid incarnation batch length")
	}
	values := make([]LifetimeValue, len(keys))
	for i, row := range rows {
		parts, err := row.ToArray()
		if valkey.IsValkeyNil(err) {
			continue
		}
		if err != nil || len(parts) != 2 {
			return nil, errors.New("invalid incarnation batch value")
		}
		value, err := parts[0].AsBytes()
		if err != nil {
			return nil, err
		}
		millis, err := parts[1].AsInt64()
		if err != nil {
			return nil, err
		}
		if millis == -2 || millis == 0 {
			continue
		}
		if millis < -1 || millis > math.MaxInt64/int64(time.Millisecond) || len(value) > maxBytes {
			return nil, errors.New("invalid incarnation batch lifetime or size")
		}
		var lifetime time.Duration
		if millis > 0 {
			lifetime = time.Duration(millis) * time.Millisecond
		}
		values[i] = LifetimeValue{Value: bytes.Clone(value), Lifetime: lifetime, Found: true}
	}
	return values, nil
}
