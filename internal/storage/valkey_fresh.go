package storage

import (
	"context"
	"errors"
	"strings"
)

// ErrDatabaseNotEmpty refuses fresh initialization over an existing KV database.
var ErrDatabaseNotEmpty = errors.New("KV database is not empty or has a different initialization claim")

// FreshDatabase supports explicit claims on an empty shared database.
// A matching retry is valid only while the claim remains the sole key.
type FreshDatabase interface {
	IncarnationProvider
	ClaimEmptyDatabase(context.Context, string, string, []byte) error
}

// ClaimEmptyDatabase binds a fresh database to one initialization operation.
// The identity check, empty-state check, and claim write form one native operation.
func (v *ValkeyStore) ClaimEmptyDatabase(ctx context.Context, identity, key string, claim []byte) error {
	if v.config.ClusterMode || !validValkeyIncarnation(identity) {
		return ErrIncarnationChanged
	}
	if key == "" || len(claim) == 0 || len(claim) > 4096 {
		return ErrInvalidMutation
	}
	const script = valkeyApprovedIncarnation + `
local size = redis.call('DBSIZE')
if size == 0 then redis.call('SET', KEYS[1], ARGV[2]); return 1 end
if size == 1 and redis.call('GET', KEYS[1]) == ARGV[2] and redis.call('PTTL', KEYS[1]) == -1 then return 1 end
return 0`
	result, err := v.client.Do(ctx, v.client.B().Eval().Script(script).Numkeys(1).Key(key).Arg(identity, string(claim)).Build()).AsInt64()
	if err != nil {
		if strings.Contains(err.Error(), "WRONGTYPE") {
			return ErrDatabaseNotEmpty
		}
		return incarnationError(err)
	}
	if result != 1 {
		return ErrDatabaseNotEmpty
	}
	return nil
}

var _ FreshDatabase = (*ValkeyStore)(nil)
