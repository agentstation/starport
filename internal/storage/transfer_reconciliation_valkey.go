package storage

import (
	"context"
	"errors"
	"strconv"
)

func (v *valkeyTransfer) ReconcileImport(ctx context.Context, claim []byte, sequence int64, previous, evidence string, mutations []CompareAndSwapMutation) (string, error) {
	receipt, encoded, err := newReconciliationReceipt(claim, sequence, previous, evidence, mutations)
	if err != nil {
		return "", err
	}
	plan, err := prepareImportReconciliation(claim, receipt, encoded, mutations, func(key string) ([]byte, error) {
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
		return "", err
	}
	// Compare all records and check command permissions before the first write.
	const script = valkeyApprovedIncarnation + `
local checks = tonumber(ARGV[2])
for i=1,checks do
 local offset = 3+(i-1)*2
 local current = redis.call('GET', KEYS[i])
 if ARGV[offset] == 'absent' then
  if current then return 0 end
 else
  if current ~= ARGV[offset+1] or redis.call('PTTL', KEYS[i]) ~= -1 then return 0 end
 end
end
for i=checks+1,#KEYS do
 local offset = 3+checks*2+(i-checks-1)*2
 if ARGV[offset] == 'delete' then
  if not redis.acl_check_cmd('DEL', KEYS[i]) then return redis.error_reply('STARPORT_RECONCILIATION_ACL') end
 else
  if not redis.acl_check_cmd('SET', KEYS[i], ARGV[offset+1]) then return redis.error_reply('STARPORT_RECONCILIATION_ACL') end
 end
end
for i=checks+1,#KEYS do
 local offset = 3+checks*2+(i-checks-1)*2
 if ARGV[offset] == 'delete' then redis.call('DEL', KEYS[i])
 else redis.call('SET', KEYS[i], ARGV[offset+1]) end
end
return 1`
	guards := append([]CompareAndSwapMutation(nil), plan.guards...)
	if len(plan.writes) > 0 {
		guards = append(guards, mutations...)
	}
	store := v.bound.store
	keys := make([]string, 0, len(guards)+len(plan.writes))
	args := []string{v.bound.identity, strconv.Itoa(len(guards))}
	for _, guard := range guards {
		keys = append(keys, store.prefix+guard.Key)
		exists := "present"
		if guard.ExpectedValue == nil {
			exists = "absent"
		}
		args = append(args, exists, string(guard.ExpectedValue))
	}
	for _, mutation := range plan.writes {
		keys = append(keys, store.prefix+mutation.Key)
		action := "set"
		if mutation.NewValue == nil {
			action = "delete"
		}
		args = append(args, action, string(mutation.NewValue))
	}
	result, err := store.do(ctx, store.client.B().Eval().Script(script).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build()).AsInt64()
	if err != nil {
		return "", transferValkeyError(err)
	}
	if result != 1 {
		return "", ErrConflict
	}
	return plan.digest, nil
}

var _ ImportReconciler = (*valkeyTransfer)(nil)
