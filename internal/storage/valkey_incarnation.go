package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/valkey-io/valkey-go"
)

// These commands and the data operation run in the same server script.
// A reconnect cannot separate the identity check from the data operation.
const valkeyIncarnation = `
local server = redis.call('INFO', 'server')
local replication = redis.call('INFO', 'replication')
local process = string.match(server, 'run_id:([^\r\n]+)')
local history = string.match(replication, 'master_replid:([^\r\n]+)')
local role = string.match(replication, 'role:([^\r\n]+)')
if not process or not history or role ~= 'master' then
  return redis.error_reply('STARPORT_INCARNATION_CHANGED')
end
local identity = process .. ':' .. history
`

const valkeyApprovedIncarnation = valkeyIncarnation + `
if identity ~= ARGV[1] then return redis.error_reply('STARPORT_INCARNATION_CHANGED') end
`

type valkeyIncarnationStore struct {
	store    *ValkeyStore
	identity string
}

// ObserveIncarnation reads the serving process and replication history identity.
// The initial recovery recipe supports one controlled primary, without cluster failover.
func (v *ValkeyStore) ObserveIncarnation(ctx context.Context) (string, error) {
	if v.config.ClusterMode {
		return "", fmt.Errorf("%w: cluster recovery is unsupported", ErrIncarnationChanged)
	}
	cmd := v.client.B().Eval().Script(valkeyIncarnation + "return identity").Numkeys(0).Build()
	id, err := v.do(ctx, cmd).ToString()
	if err != nil {
		return "", incarnationError(err)
	}
	if !validValkeyIncarnation(id) {
		return "", ErrIncarnationChanged
	}
	return id, nil
}

// BindIncarnation checks the approved identity and retains it for each operation.
func (v *ValkeyStore) BindIncarnation(ctx context.Context, approved string) (IncarnationStore, error) {
	if !validValkeyIncarnation(approved) {
		return nil, ErrIncarnationChanged
	}
	observed, err := v.ObserveIncarnation(ctx)
	if err != nil {
		return nil, err
	}
	if observed != approved {
		return nil, ErrIncarnationChanged
	}
	return &valkeyIncarnationStore{store: v, identity: approved}, nil
}

func validValkeyIncarnation(id string) bool {
	if len(id) != 81 || id[40] != ':' {
		return false
	}
	for i, c := range id {
		if i != 40 && (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func incarnationError(err error) error {
	if err != nil && strings.Contains(err.Error(), "STARPORT_INCARNATION_CHANGED") {
		return ErrIncarnationChanged
	}
	return err
}

func (b *valkeyIncarnationStore) ReadWithLifetime(ctx context.Context, key string, maxBytes int) ([]byte, time.Duration, error) {
	if maxBytes <= 0 {
		return nil, 0, ErrInvalidReadLimit
	}
	if key == "" {
		return nil, 0, ErrInvalidKey
	}
	const script = valkeyApprovedIncarnation + `
if redis.call('EXISTS', KEYS[1]) == 0 then return false end
if redis.call('STRLEN', KEYS[1]) > tonumber(ARGV[2]) then return redis.error_reply('STARPORT_VALUE_TOO_LARGE') end
return {redis.call('GET', KEYS[1]), redis.call('PTTL', KEYS[1])}`
	cmd := b.store.client.B().Eval().Script(script).Numkeys(1).Key(key).Arg(b.identity, strconv.Itoa(maxBytes)).Build()
	values, err := b.store.do(ctx, cmd).ToArray()
	if valkey.IsValkeyNil(err) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		if strings.Contains(err.Error(), "STARPORT_VALUE_TOO_LARGE") {
			return nil, 0, ErrValueTooLarge
		}
		return nil, 0, incarnationError(err)
	}
	if len(values) != 2 {
		return nil, 0, errors.New("invalid incarnation value response")
	}
	value, err := values[0].AsBytes()
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
		return nil, 0, errors.New("invalid incarnation value lifetime")
	}
	return bytes.Clone(value), time.Duration(millis) * time.Millisecond, nil
}

func (b *valkeyIncarnationStore) CompareAndSwap(ctx context.Context, mutations []CompareAndSwapMutation, liveKeys ...string) error {
	if err := validateCompareAndSwapMutations(mutations); err != nil {
		return err
	}
	live := make(map[string]bool, len(liveKeys))
	for _, key := range liveKeys {
		live[key] = false
	}
	keys := make([]string, 0, len(mutations))
	args := []string{b.identity}
	for _, m := range mutations {
		requireLive := "0"
		if _, ok := live[m.Key]; ok {
			if m.ExpectedValue == nil {
				return ErrInvalidMutation
			}
			live[m.Key], requireLive = true, "1"
		}
		exists, next := "1", "1"
		if m.ExpectedValue == nil {
			exists = "0"
		}
		if m.NewValue == nil {
			next = "0"
		}
		millis := m.TTL.Milliseconds()
		if m.TTL > 0 && millis == 0 {
			millis = 1
		}
		keys = append(keys, m.Key)
		args = append(args, exists, string(m.ExpectedValue), next, string(m.NewValue), strconv.FormatInt(millis, 10), requireLive)
	}
	for _, found := range live {
		if !found {
			return ErrInvalidMutation
		}
	}
	cmd := b.store.client.B().Eval().Script(valkeyIncarnationCAS).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build()
	result, err := b.store.do(ctx, cmd).AsInt64()
	if err != nil {
		return incarnationError(err)
	}
	if result != 1 {
		return ErrConflict
	}
	return nil
}

const valkeyIncarnationCAS = valkeyApprovedIncarnation + `
for i, key in ipairs(KEYS) do
  local n = 1 + (i - 1) * 6
  local current = redis.call('GET', key)
  if ARGV[n + 1] == '0' then
    if current then return 0 end
  elseif current ~= ARGV[n + 2] then return 0 end
  if ARGV[n + 6] == '1' and redis.call('PTTL', key) <= 0 then return 0 end
end
for i, key in ipairs(KEYS) do
  local n = 1 + (i - 1) * 6
  if ARGV[n + 3] == '0' then redis.call('DEL', key)
  elseif tonumber(ARGV[n + 5]) > 0 then
    redis.call('SET', key, ARGV[n + 4], 'PX', ARGV[n + 5])
  else redis.call('SET', key, ARGV[n + 4], 'KEEPTTL') end
end
return 1
`

var _ IncarnationProvider = (*ValkeyStore)(nil)
