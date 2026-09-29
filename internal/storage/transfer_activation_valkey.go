package storage

import "context"

func (v *valkeyTransfer) ActivateImport(ctx context.Context, claim []byte, decisionSHA256 string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, receipt, err := activationReceipt(claim, decisionSHA256)
	if err != nil {
		return err
	}
	const script = valkeyApprovedIncarnation + `
local barrier = redis.call('GET', KEYS[1])
local current = redis.call('GET', KEYS[2])
local history = redis.call('GET', KEYS[3])
if not barrier then
 if current == ARGV[3] and history == ARGV[3] and redis.call('PTTL', KEYS[2]) == -1 and redis.call('PTTL', KEYS[3]) == -1 then return 1 end
 return 0
end
if barrier ~= ARGV[2] or redis.call('PTTL', KEYS[1]) ~= -1 or current or history then return 0 end
redis.call('MSET', KEYS[2], ARGV[3], KEYS[3], ARGV[3])
redis.call('DEL', KEYS[1])
return 1`
	store := v.bound.store
	result, err := store.do(ctx, store.client.B().Eval().Script(script).Numkeys(3).
		Key(store.prefix+TransferBarrierKey, store.prefix+transferActivationCurrent, store.prefix+key).
		Arg(v.bound.identity, string(claim), string(receipt)).Build()).AsInt64()
	if err != nil {
		return transferValkeyError(err)
	}
	if result != 1 {
		return ErrConflict
	}
	return nil
}

var _ ImportActivator = (*valkeyTransfer)(nil)
