package storage

import (
	"context"
	"errors"
)

func (v *valkeyTransfer) ActivateImport(ctx context.Context, claim []byte, decisionSHA256 string) error {
	return v.activateImport(ctx, claim, nil, decisionSHA256, false)
}

func (v *valkeyTransfer) ActivateImportAt(ctx context.Context, claim []byte, position ImportReplayPosition, decisionSHA256 string) error {
	return v.activateImport(ctx, claim, &position, decisionSHA256, false)
}

func (v *valkeyTransfer) CheckActivatedImportAt(ctx context.Context, claim []byte, position ImportReplayPosition, decisionSHA256 string) error {
	return v.activateImport(ctx, claim, &position, decisionSHA256, true)
}

func (v *valkeyTransfer) activateImport(ctx context.Context, claim []byte, position *ImportReplayPosition, decisionSHA256 string, inspect bool) error {
	if ctx == nil {
		return ErrInvalidMutation
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key, receipt, err := activationReceiptAt(claim, position, decisionSHA256)
	if err != nil {
		return err
	}
	var guards []CompareAndSwapMutation
	if position != nil {
		guards, err = activationPositionGuards(claim, *position, func(key string) ([]byte, error) {
			value, ttl, err := v.bound.ReadWithLifetime(ctx, key, 4096)
			if errors.Is(err, ErrNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			if ttl != 0 {
				return nil, ErrConflict
			}
			return value, nil
		})
		if err != nil {
			return err
		}
	}
	const script = valkeyApprovedIncarnation + `
for i=4,#KEYS do
 local offset = 5+(i-4)*2
 local value = redis.call('GET', KEYS[i])
 if ARGV[offset] == 'absent' then
  if value then return 0 end
 else
  if value ~= ARGV[offset+1] or redis.call('PTTL', KEYS[i]) ~= -1 then return 0 end
 end
end
local barrier = redis.call('GET', KEYS[1])
local current = redis.call('GET', KEYS[2])
local history = redis.call('GET', KEYS[3])
if not barrier then
 if current == ARGV[3] and history == ARGV[3] and redis.call('PTTL', KEYS[2]) == -1 and redis.call('PTTL', KEYS[3]) == -1 then return 1 end
 return 0
end
if ARGV[4] == 'inspect' then return 0 end
if barrier ~= ARGV[2] or redis.call('PTTL', KEYS[1]) ~= -1 or current or history then return 0 end
if not redis.acl_check_cmd('MSET', KEYS[2], ARGV[3], KEYS[3], ARGV[3]) or not redis.acl_check_cmd('DEL', KEYS[1]) then
 return redis.error_reply('STARPORT_ACTIVATION_ACL')
end
redis.call('MSET', KEYS[2], ARGV[3], KEYS[3], ARGV[3])
redis.call('DEL', KEYS[1])
return 1`
	store := v.bound.store
	keys := []string{store.prefix + TransferBarrierKey, store.prefix + transferActivationCurrent, store.prefix + key}
	mode := "activate"
	if inspect {
		mode = "inspect"
	}
	args := []string{v.bound.identity, string(claim), string(receipt), mode}
	for _, guard := range guards {
		keys = append(keys, store.prefix+guard.Key)
		exists := importGuardPresent
		if guard.ExpectedValue == nil {
			exists = importGuardAbsent
		}
		args = append(args, exists, string(guard.ExpectedValue))
	}
	result, err := store.do(ctx, store.client.B().Eval().Script(script).Numkeys(int64(len(keys))).
		Key(keys...).Arg(args...).Build()).AsInt64()
	if err != nil {
		return transferValkeyError(err)
	}
	if result != 1 {
		return ErrConflict
	}
	return nil
}

var _ ImportActivator = (*valkeyTransfer)(nil)
var _ ImportReplayActivator = (*valkeyTransfer)(nil)

var _ ImportActivationInspector = (*valkeyTransfer)(nil)
