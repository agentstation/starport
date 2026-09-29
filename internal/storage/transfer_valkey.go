package storage

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/valkey-io/valkey-go"
)

type valkeyTransfer struct{ bound *valkeyIncarnationStore }

func (*valkeyTransfer) ExpiryResolution() time.Duration { return time.Millisecond }

func (v *valkeyTransfer) Enumerate(ctx context.Context, yield func(TransferRecord) error) error {
	if yield == nil {
		return ErrInvalidMutation
	}
	store := v.bound.store
	pattern := strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[", "]", "\\]").Replace(store.prefix) + "*"
	cursor := "0"
	const scan = valkeyApprovedIncarnation + `
if redis.call('EXISTS', KEYS[1]) ~= 0 then return redis.error_reply('STARPORT_IMPORT_RESTRICTED') end
return redis.call('SCAN', ARGV[2], 'MATCH', ARGV[3], 'COUNT', 256)`
	for {
		response, err := store.do(ctx, store.client.B().Eval().Script(scan).Numkeys(1).Key(store.prefix+TransferBarrierKey).Arg(v.bound.identity, cursor, pattern).Build()).AsScanEntry()
		if err != nil {
			return transferValkeyError(err)
		}
		for _, physical := range response.Elements {
			key, ok := strings.CutPrefix(physical, store.prefix)
			if !ok || len(key) == 0 || len(key) > TransferMaxKeyBytes {
				return ErrInvalidKey
			}
			if key == transferActivationCurrent {
				continue
			}
			record, err := v.read(ctx, key)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if err := yield(record); err != nil {
				return err
			}
		}
		if response.Cursor == 0 {
			break
		}
		cursor = strconv.FormatUint(response.Cursor, 10)
	}
	// A replacement during the last callback must not produce a successful export.
	_, err := store.BindIncarnation(ctx, v.bound.identity)
	return err
}

func (v *valkeyTransfer) read(ctx context.Context, key string) (TransferRecord, error) {
	const script = valkeyApprovedIncarnation + `
if redis.call('EXISTS', KEYS[2]) ~= 0 then return redis.error_reply('STARPORT_IMPORT_RESTRICTED') end
if redis.call('EXISTS', KEYS[1]) == 0 then return false end
if redis.call('STRLEN', KEYS[1]) > tonumber(ARGV[2]) then return redis.error_reply('STARPORT_VALUE_TOO_LARGE') end
return {redis.call('GET', KEYS[1]), redis.call('PEXPIRETIME', KEYS[1])}`
	store := v.bound.store
	values, err := store.do(ctx, store.client.B().Eval().Script(script).Numkeys(2).Key(store.prefix+key, store.prefix+TransferBarrierKey).Arg(v.bound.identity, strconv.Itoa(TransferMaxValueBytes)).Build()).ToArray()
	if valkey.IsValkeyNil(err) {
		return TransferRecord{}, ErrNotFound
	}
	if err != nil {
		return TransferRecord{}, transferValkeyError(err)
	}
	if len(values) != 2 {
		return TransferRecord{}, errors.New("invalid record transfer response")
	}
	value, err := values[0].AsBytes()
	if err != nil {
		return TransferRecord{}, err
	}
	expires, err := values[1].AsInt64()
	if err != nil {
		return TransferRecord{}, err
	}
	if expires == -2 {
		return TransferRecord{}, ErrNotFound
	}
	if expires == -1 {
		expires = 0
	}
	record := TransferRecord{Key: key, Value: bytes.Clone(value), ExpiresAtMillis: expires}
	return record, record.Validate()
}

func (v *valkeyTransfer) Claim(ctx context.Context, claim []byte) error {
	if err := validateTransferClaim(claim); err != nil {
		return err
	}
	_, _, err := v.bound.ReadWithLifetime(ctx, transferActivationCurrent, 256)
	if err == nil {
		return ErrConflict
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	current, ttl, err := v.bound.ReadWithLifetime(ctx, TransferBarrierKey, 4096)
	if err == nil {
		if ttl != 0 || !bytes.Equal(current, claim) {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	// The coordinator must fence target writers. This scan establishes emptiness.
	// the following native CAS chooses between concurrent import owners.
	err = v.Enumerate(ctx, func(TransferRecord) error { return ErrDatabaseNotEmpty })
	if err != nil {
		return err
	}
	const claimScript = valkeyApprovedIncarnation + `
if redis.call('EXISTS', KEYS[1], KEYS[2]) ~= 0 then return 0 end
redis.call('SET', KEYS[1], ARGV[2])
return 1`
	store := v.bound.store
	result, err := store.do(ctx, store.client.B().Eval().Script(claimScript).Numkeys(2).
		Key(store.prefix+TransferBarrierKey, store.prefix+transferActivationCurrent).
		Arg(v.bound.identity, string(claim)).Build()).AsInt64()
	if err != nil {
		return transferValkeyError(err)
	}
	if result != 1 {
		return ErrConflict
	}
	return nil
}

func (v *valkeyTransfer) Import(ctx context.Context, claim []byte, record TransferRecord) error {
	if err := validateTransferClaim(claim); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	const script = valkeyApprovedIncarnation + `
if redis.call('EXISTS', KEYS[3]) ~= 0 or redis.call('GET', KEYS[1]) ~= ARGV[2] or redis.call('PTTL', KEYS[1]) ~= -1 then return 0 end
local current = redis.call('GET', KEYS[2])
local expected = tonumber(ARGV[4])
if current then
  local expires = redis.call('PEXPIRETIME', KEYS[2])
  if expires == -1 then expires = 0 end
  if current == ARGV[3] and expires == expected then return 1 end
  return 0
end
if expected == 0 then redis.call('SET', KEYS[2], ARGV[3])
else
  local now = redis.call('TIME')
  if expected > tonumber(now[1])*1000 + math.floor(tonumber(now[2])/1000) then
    redis.call('SET', KEYS[2], ARGV[3], 'PXAT', ARGV[4])
  end
end
return 1`
	store := v.bound.store
	result, err := store.do(ctx, store.client.B().Eval().Script(script).Numkeys(3).Key(store.prefix+TransferBarrierKey, store.prefix+record.Key, store.prefix+transferActivationCurrent).Arg(v.bound.identity, string(claim), string(record.Value), strconv.FormatInt(record.ExpiresAtMillis, 10)).Build()).AsInt64()
	if err != nil {
		return transferValkeyError(err)
	}
	if result != 1 {
		return ErrConflict
	}
	return nil
}

func transferValkeyError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case strings.Contains(err.Error(), "STARPORT_IMPORT_RESTRICTED"):
		return ErrImportRestricted
	case strings.Contains(err.Error(), "STARPORT_VALUE_TOO_LARGE"):
		return ErrValueTooLarge
	default:
		return incarnationError(err)
	}
}
