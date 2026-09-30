package storage

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/valkey-io/valkey-go"
)

// Every command validates both the selected backend and the complete native cursor.
const valkeyImportInspectionGuard = valkeyApprovedIncarnation + `
local count=tonumber(ARGV[2])
for i=1,count do
 local value=redis.call('GET',KEYS[i])
 local present=ARGV[2*i+1]
 if present=='1' then
  if not value or value~=ARGV[2*i+2] or redis.call('PTTL',KEYS[i])~=-1 then return redis.error_reply('STARPORT_IMPORT_RESTRICTED') end
 else
  if value then return redis.error_reply('STARPORT_IMPORT_RESTRICTED') end
 end
end
local offset=2+count*2
`

func (v *valkeyTransfer) InspectImport(ctx context.Context, claim []byte, position ImportReplayPosition, yield func(TransferRecord) error) error {
	if ctx == nil || yield == nil {
		return ErrInvalidMutation
	}
	guards, err := v.importInspectionGuards(ctx, claim, position)
	if err != nil {
		return err
	}
	store := v.bound.store
	keys, args := v.importInspectionArguments(guards)
	pattern := strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[", "]", "\\]").Replace(store.prefix) + "*"
	cursor := "0"
	for {
		scanArgs := append(append([]string{}, args...), cursor, pattern)
		response, err := store.do(ctx, store.client.B().Eval().Script(valkeyImportInspectionGuard+`return redis.call('SCAN',ARGV[offset+1],'MATCH',ARGV[offset+2],'COUNT',256)`).Numkeys(int64(len(keys))).Key(keys...).Arg(scanArgs...).Build()).AsScanEntry()
		if err != nil {
			return transferValkeyError(err)
		}
		for _, physical := range response.Elements {
			key, ok := strings.CutPrefix(physical, store.prefix)
			if !ok || len(key) == 0 || len(key) > TransferMaxKeyBytes {
				return ErrInvalidKey
			}
			if importControlKey(key) {
				continue
			}
			record, err := v.readImportInspection(ctx, key, keys, args, TransferMaxValueBytes)
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
	_, err = store.do(ctx, store.client.B().Eval().Script(valkeyImportInspectionGuard+`return 1`).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build()).AsInt64()
	return transferValkeyError(err)
}

func (v *valkeyTransfer) readImportInspection(ctx context.Context, key string, guards, args []string, limit int) (TransferRecord, error) {
	const read = `
local key=KEYS[count+1]
if redis.call('EXISTS',key)==0 then return false end
if redis.call('STRLEN',key)>tonumber(ARGV[offset+1]) then return redis.error_reply('STARPORT_VALUE_TOO_LARGE') end
return {redis.call('GET',key),redis.call('PEXPIRETIME',key)}`
	store := v.bound.store
	keys := append(append([]string{}, guards...), store.prefix+key)
	readArgs := append(append([]string{}, args...), strconv.Itoa(limit))
	values, err := store.do(ctx, store.client.B().Eval().Script(valkeyImportInspectionGuard+read).Numkeys(int64(len(keys))).Key(keys...).Arg(readArgs...).Build()).ToArray()
	if valkey.IsValkeyNil(err) {
		return TransferRecord{}, ErrNotFound
	}
	if err != nil {
		return TransferRecord{}, transferValkeyError(err)
	}
	if len(values) != 2 {
		return TransferRecord{}, ErrInvalidMutation
	}
	value, err := values[0].AsBytes()
	if err != nil {
		return TransferRecord{}, err
	}
	expiry, err := values[1].AsInt64()
	if err != nil {
		return TransferRecord{}, err
	}
	if expiry == -2 {
		return TransferRecord{}, ErrNotFound
	}
	if expiry == -1 {
		expiry = 0
	}
	record := TransferRecord{Key: key, Value: bytes.Clone(value), ExpiresAtMillis: expiry}
	return record, record.Validate()
}

var _ ImportInspector = (*valkeyTransfer)(nil)

func (v *valkeyTransfer) importInspectionGuards(ctx context.Context, claim []byte, position ImportReplayPosition) ([]CompareAndSwapMutation, error) {
	return prepareImportInspection(claim, position, func(key string) ([]byte, error) {
		data, ttl, err := v.bound.ReadWithLifetime(ctx, key, 4096)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if ttl != 0 {
			return nil, ErrConflict
		}
		return data, nil
	})
}

func (v *valkeyTransfer) importInspectionArguments(guards []CompareAndSwapMutation) ([]string, []string) {
	store := v.bound.store
	keys := make([]string, 0, len(guards)+1)
	args := []string{v.bound.identity, strconv.Itoa(len(guards))}
	for _, guard := range guards {
		keys = append(keys, store.prefix+guard.Key)
		present := "0"
		if guard.ExpectedValue != nil {
			present = "1"
		}
		args = append(args, present, string(guard.ExpectedValue))
	}
	return keys, args
}
