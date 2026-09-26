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

// ReadWithLifetime binds a bounded value and its PTTL in one server operation.
func (v *ValkeyStore) ReadWithLifetime(ctx context.Context, key string, maxBytes int) ([]byte, time.Duration, error) {
	if maxBytes <= 0 {
		return nil, 0, ErrInvalidReadLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	const script = `if redis.call('EXISTS', KEYS[1]) == 0 then return false end
if redis.call('STRLEN', KEYS[1]) > tonumber(ARGV[1]) then return redis.error_reply('STARPORT_VALUE_TOO_LARGE') end
return {redis.call('GET', KEYS[1]), redis.call('PTTL', KEYS[1])}`
	cmd := v.client.B().Eval().Script(script).Numkeys(1).Key(key).Arg(strconv.Itoa(maxBytes)).Build()
	values, err := v.client.Do(ctx, cmd).ToArray()
	if valkey.IsValkeyNil(err) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		if strings.Contains(err.Error(), "STARPORT_VALUE_TOO_LARGE") {
			return nil, 0, ErrValueTooLarge
		}
		return nil, 0, err
	}
	if len(values) != 2 {
		return nil, 0, errors.New("invalid value lifetime response")
	}
	value, err := values[0].AsBytes()
	if valkey.IsValkeyNil(err) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	millis, err := values[1].AsInt64()
	if err != nil {
		return nil, 0, err
	}
	if millis == -2 || millis == 0 {
		return nil, 0, ErrNotFound
	}
	if millis == -1 {
		return bytes.Clone(value), 0, nil
	}
	if millis < 0 || millis > math.MaxInt64/int64(time.Millisecond) {
		return nil, 0, errors.New("invalid value lifetime")
	}
	return bytes.Clone(value), time.Duration(millis) * time.Millisecond, nil
}
