package storage

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
)

func (v *valkeyTransfer) ObserveImportControls(ctx context.Context) (ImportControls, error) {
	if ctx == nil {
		return ImportControls{}, ErrInvalidMutation
	}
	// One script reads the barrier and both roots, so the observation is consistent.
	const script = valkeyApprovedIncarnation + `
if redis.call('EXISTS', KEYS[1]) ~= 0 then return redis.error_reply('STARPORT_IMPORT_RESTRICTED') end
local result = {}
for i=2,3 do
 local value = redis.call('GET', KEYS[i])
 if value then
  if string.len(value) > tonumber(ARGV[2]) then return redis.error_reply('STARPORT_VALUE_TOO_LARGE') end
  if redis.call('PTTL', KEYS[i]) ~= -1 then return redis.error_reply('STARPORT_IMPORT_CONFLICT') end
  table.insert(result, '1')
  table.insert(result, value)
 else
  table.insert(result, '0')
  table.insert(result, '')
 end
end
return result`
	store := v.bound.store
	values, err := store.do(ctx, store.client.B().Eval().Script(script).Numkeys(3).
		Key(store.prefix+TransferBarrierKey, store.prefix+transferActivationCurrent, store.prefix+transferReconciliationCurrent).
		Arg(v.bound.identity, strconv.Itoa(importControlMaxBytes)).Build()).AsStrSlice()
	if err != nil {
		if strings.Contains(err.Error(), "STARPORT_IMPORT_CONFLICT") {
			return ImportControls{}, ErrConflict
		}
		return ImportControls{}, transferValkeyError(err)
	}
	if len(values) != 4 {
		return ImportControls{}, errors.New("invalid import control response")
	}
	controls := ImportControls{ActivationPresent: values[0] == "1", ReconciliationPresent: values[2] == "1"}
	if controls.ActivationPresent {
		controls.Activation = []byte(values[1])
	}
	if controls.ReconciliationPresent {
		controls.Reconciliation = []byte(values[3])
	}
	return controls, controls.validate()
}

func (v *valkeyTransfer) ClaimPopulated(ctx context.Context, claim []byte, controls ImportControls, captured CapturedRecordSource) error {
	if ctx == nil || captured == nil {
		return ErrInvalidMutation
	}
	if err := validateTransferClaim(claim); err != nil {
		return err
	}
	if err := controls.validate(); err != nil {
		return err
	}
	barrier, ttl, err := v.bound.ReadWithLifetime(ctx, TransferBarrierKey, importControlMaxBytes)
	if err == nil {
		if ttl != 0 || !bytes.Equal(barrier, claim) {
			return ErrConflict
		}
		// An exact retry finds its own barrier and writes nothing.
		return v.checkPopulatedClaim(ctx, claim, controls, captured)
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	observed, err := v.ObserveImportControls(ctx)
	if err != nil {
		return err
	}
	if !observed.equal(controls) {
		return ErrConflict
	}
	// The census runs while the barrier stays absent and both roots keep their observed values.
	keys, args := v.importInspectionArguments(append([]CompareAndSwapMutation{{Key: TransferBarrierKey}}, controls.guards()...))
	census, err := v.populatedCensus(ctx, keys, args, captured, TransferRecord{})
	if err != nil {
		return err
	}
	key, receipt, err := populatedClaimReceiptAt(claim, controls, census)
	if err != nil {
		return err
	}
	const script = valkeyApprovedIncarnation + `
if redis.call('EXISTS', KEYS[1], KEYS[4]) ~= 0 then return 0 end
for i=2,3 do
 local offset = 3+(i-2)*2
 local value = redis.call('GET', KEYS[i])
 if ARGV[offset] == 'absent' then
  if value then return 0 end
 else
  if value ~= ARGV[offset+1] or redis.call('PTTL', KEYS[i]) ~= -1 then return 0 end
 end
end
if not redis.acl_check_cmd('SET', KEYS[4], ARGV[7]) or not redis.acl_check_cmd('SET', KEYS[1], ARGV[2]) or not redis.acl_check_cmd('DEL', KEYS[2], KEYS[3]) then
 return redis.error_reply('STARPORT_POPULATED_ACL')
end
redis.call('SET', KEYS[4], ARGV[7])
redis.call('SET', KEYS[1], ARGV[2])
redis.call('DEL', KEYS[2], KEYS[3])
return 1`
	store := v.bound.store
	scriptArgs := []string{v.bound.identity, string(claim)}
	for _, guard := range controls.guards() {
		exists := importGuardPresent
		if guard.ExpectedValue == nil {
			exists = importGuardAbsent
		}
		scriptArgs = append(scriptArgs, exists, string(guard.ExpectedValue))
	}
	scriptArgs = append(scriptArgs, string(receipt))
	result, err := store.do(ctx, store.client.B().Eval().Script(script).Numkeys(4).
		Key(store.prefix+TransferBarrierKey, store.prefix+transferActivationCurrent, store.prefix+transferReconciliationCurrent, store.prefix+key).
		Arg(scriptArgs...).Build()).AsInt64()
	if err != nil {
		return transferValkeyError(err)
	}
	if result != 1 {
		return ErrConflict
	}
	return v.checkPopulatedClaim(ctx, claim, controls, captured)
}

// checkPopulatedClaim repeats the census under the barrier at replay position zero.
// The closure receipt is the only permitted record that the capture does not hold.
func (v *valkeyTransfer) checkPopulatedClaim(ctx context.Context, claim []byte, controls ImportControls, captured CapturedRecordSource) error {
	guards, err := v.importInspectionGuards(ctx, claim, ImportReplayPosition{})
	if err != nil {
		return err
	}
	key := transferPopulatedPrefix + reconciliationDigest(claim)
	stored, ttl, err := v.bound.ReadWithLifetime(ctx, key, importControlMaxBytes)
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if ttl != 0 {
		return ErrConflict
	}
	keys, args := v.importInspectionArguments(append(guards, CompareAndSwapMutation{Key: key, ExpectedValue: stored}))
	census, err := v.populatedCensus(ctx, keys, args, captured, TransferRecord{Key: key, Value: stored})
	if err != nil {
		return err
	}
	_, receipt, err := populatedClaimReceiptAt(claim, controls, census)
	if err != nil {
		return err
	}
	if !bytes.Equal(receipt, stored) {
		return ErrConflict
	}
	return nil
}

// populatedCensus requires exact live equality with the capture and returns the capture digest.
// A captured record can be absent only when its original expiry is not later than server time.
func (v *valkeyTransfer) populatedCensus(ctx context.Context, keys, args []string, captured CapturedRecordSource, closure TransferRecord) (string, error) {
	census := newPopulatedCensus()
	err := captured.Enumerate(ctx, func(record TransferRecord) error {
		if err := census.add(record); err != nil {
			return err
		}
		return v.checkCapturedRecord(ctx, record, keys, args)
	})
	if err != nil {
		return "", err
	}
	err = v.inspectGuarded(ctx, keys, args, func(live TransferRecord) error {
		if closure.Key != "" && live.Key == closure.Key {
			if !bytes.Equal(live.Value, closure.Value) || live.ExpiresAtMillis != 0 {
				return ErrConflict
			}
			return nil
		}
		record, err := captured.ReadCaptured(ctx, live.Key, TransferMaxValueBytes)
		if errors.Is(err, ErrNotFound) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if record.Key != live.Key || !bytes.Equal(record.Value, live.Value) || record.ExpiresAtMillis != live.ExpiresAtMillis {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return census.sum(), nil
}

func (v *valkeyTransfer) checkCapturedRecord(ctx context.Context, record TransferRecord, guards, args []string) error {
	const check = `
local key=KEYS[count+1]
local expiry=tonumber(ARGV[offset+2])
local value=redis.call('GET',key)
if value then
 local expires=redis.call('PEXPIRETIME',key)
 if expires==-1 then expires=0 end
 if value~=ARGV[offset+1] or expires~=expiry then return 0 end
 return 1
end
if expiry==0 then return 0 end
local now=redis.call('TIME')
if expiry>tonumber(now[1])*1000+math.floor(tonumber(now[2])/1000) then return 0 end
return 1`
	store := v.bound.store
	keys := append(append([]string{}, guards...), store.prefix+record.Key)
	checkArgs := append(append([]string{}, args...), string(record.Value), strconv.FormatInt(record.ExpiresAtMillis, 10))
	result, err := store.do(ctx, store.client.B().Eval().Script(valkeyImportInspectionGuard+check).Numkeys(int64(len(keys))).Key(keys...).Arg(checkArgs...).Build()).AsInt64()
	if err != nil {
		return transferValkeyError(err)
	}
	if result != 1 {
		return ErrConflict
	}
	return nil
}

var _ PopulatedImportClaimer = (*valkeyTransfer)(nil)
