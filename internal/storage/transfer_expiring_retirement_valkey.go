package storage

import (
	"context"
	"errors"
	"strconv"
)

func (v *valkeyTransfer) ReconcileExpiringImport(ctx context.Context, claim []byte, sequence int64, previous, evidence string, records []TransferRecord) (string, error) {
	records, err := copyExpiringImportRecords(ctx, records)
	if err != nil {
		return "", err
	}
	receipt, encoded, mutations, err := expiringReconciliationReceipt(claim, sequence, previous, evidence, records)
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
	const script = valkeyApprovedIncarnation + `
local checks = tonumber(ARGV[2])
local expiring = tonumber(ARGV[3])
for i=1,checks do
 local offset = 4+(i-1)*2
 local current = redis.call('GET', KEYS[i])
 if ARGV[offset] == 'absent' then
  if current then return 0 end
 else
  if current ~= ARGV[offset+1] or redis.call('PTTL', KEYS[i]) ~= -1 then return 0 end
 end
end
local now = redis.call('TIME')
local millis = tonumber(now[1])*1000 + math.floor(tonumber(now[2])/1000)
for i=1,expiring do
 local offset = 4+checks*2+(i-1)*2
 local key = KEYS[checks+i]
 local current = redis.call('GET', key)
 local expiry = tonumber(ARGV[offset+1])
 if current then
  if current ~= ARGV[offset] or redis.call('PEXPIRETIME', key) ~= expiry then return 0 end
 elseif expiry > millis then return 0 end
end
for i=checks+expiring+1,#KEYS do
 local offset = 4+checks*2+expiring*2+(i-checks-expiring-1)*2
 if ARGV[offset] == 'delete' then
  if not redis.acl_check_cmd('DEL', KEYS[i]) then return redis.error_reply('STARPORT_RECONCILIATION_ACL') end
 else
  if not redis.acl_check_cmd('SET', KEYS[i], ARGV[offset+1]) then return redis.error_reply('STARPORT_RECONCILIATION_ACL') end
 end
end
for i=checks+expiring+1,#KEYS do
 local offset = 4+checks*2+expiring*2+(i-checks-expiring-1)*2
 if ARGV[offset] == 'delete' then redis.call('DEL', KEYS[i])
 else redis.call('SET', KEYS[i], ARGV[offset+1]) end
end
return 1`
	store := v.bound.store
	expiring := records
	if len(plan.writes) == 0 {
		expiring = nil
	}
	keys := make([]string, 0, len(plan.guards)+len(expiring)+len(plan.writes))
	args := []string{v.bound.identity, strconv.Itoa(len(plan.guards)), strconv.Itoa(len(expiring))}
	for _, guard := range plan.guards {
		keys = append(keys, store.prefix+guard.Key)
		exists := importGuardPresent
		if guard.ExpectedValue == nil {
			exists = importGuardAbsent
		}
		args = append(args, exists, string(guard.ExpectedValue))
	}
	for _, record := range expiring {
		keys = append(keys, store.prefix+record.Key)
		args = append(args, string(record.Value), strconv.FormatInt(record.ExpiresAtMillis, 10))
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

var _ ImportExpiringRetirer = (*valkeyTransfer)(nil)
