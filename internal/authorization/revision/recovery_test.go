package revision

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRevisionRecoveryTypedEvidence(t *testing.T) {
	authority := RecoveryAuthority{RecoveryID: "accepted-recovery", Epoch: "fresh-epoch"}
	before := Stamp{Epoch: "old-epoch", Sequence: 19}
	sqlTransition, err := NewSQLRecoveryTransition(&before, authority)
	require.NoError(t, err)
	sqlDigest, err := sqlTransition.Digest()
	require.NoError(t, err)
	before.Epoch = "changed-after-construction"
	repeated, err := sqlTransition.Digest()
	require.NoError(t, err)
	require.Equal(t, sqlDigest, repeated)
	raw, err := json.Marshal(sqlTransition)
	require.NoError(t, err)
	var decodedSQL SQLRecoveryTransition
	require.NoError(t, json.Unmarshal(raw, &decodedSQL))
	repeated, err = decodedSQL.Digest()
	require.NoError(t, err)
	require.Equal(t, sqlDigest, repeated)
	kvTransition, err := NewKVRecoveryTransition(strings.Repeat("a", 64), authority)
	require.NoError(t, err)
	kvDigest, err := kvTransition.Digest()
	require.NoError(t, err)
	raw, err = json.Marshal(kvTransition)
	require.NoError(t, err)
	var decodedKV KVRecoveryTransition
	require.NoError(t, json.Unmarshal(raw, &decodedKV))
	repeated, err = decodedKV.Digest()
	require.NoError(t, err)
	require.Equal(t, kvDigest, repeated)
	changed, err := NewKVRecoveryTransition(strings.Repeat("a", 64), RecoveryAuthority{RecoveryID: "different-recovery", Epoch: authority.Epoch})
	require.NoError(t, err)
	different, err := changed.Digest()
	require.NoError(t, err)
	require.NotEqual(t, kvDigest, different)
	for _, value := range []any{kvTransition, &kvTransition, sqlTransition, &sqlTransition} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
			require.Equal(t, "<private authorization revision recovery evidence>", fmt.Sprintf(verb, value))
		}
		unexpected := fmt.Sprintf("%p", value)
		require.NotContains(t, unexpected, authority.RecoveryID)
		require.NotContains(t, unexpected, authority.Epoch)
		require.NotContains(t, unexpected, strings.Repeat("a", 64))
	}
}

func TestRevisionRecoveryRefusesInvalidInputBeforeIO(t *testing.T) {
	for _, authority := range []RecoveryAuthority{{}, {RecoveryID: "accepted"}, {Epoch: "fresh"}, {RecoveryID: "recovery\n", Epoch: "fresh"}, {RecoveryID: "recovery", Epoch: strings.Repeat("e", 257)}} {
		_, err := NewKVRecoveryTransition("", authority)
		require.ErrorIs(t, err, ErrRecoveryConflict)
		_, err = NewSQLRecoveryTransition(nil, authority)
		require.ErrorIs(t, err, ErrRecoveryConflict)
	}
	authority := RecoveryAuthority{RecoveryID: "accepted", Epoch: "fresh"}
	for _, stamp := range []Stamp{{Epoch: "fresh", Sequence: 1}, {Sequence: 1}, {Epoch: "old"}, {Epoch: "old", Sequence: math.MaxUint64}} {
		_, err := NewSQLRecoveryTransition(&stamp, authority)
		require.ErrorIs(t, err, ErrRecoveryConflict)
	}
	for _, digest := range []string{"not-a-digest", strings.Repeat("A", 64), strings.Repeat("a", 63)} {
		_, err := NewKVRecoveryTransition(digest, authority)
		require.ErrorIs(t, err, ErrRecoveryConflict)
	}
	var kvZero KVRecoveryTransition
	_, err := PrepareKVRecovery(t.Context(), nil, kvZero)
	require.ErrorIs(t, err, ErrRecoveryConflict)
	var sqlZero SQLRecoveryTransition
	require.ErrorIs(t, ApplySQLRecovery(t.Context(), nil, nil, sqlZero), ErrRecoveryConflict)
	valid, err := NewKVRecoveryTransition("", authority)
	require.NoError(t, err)
	_, err = PrepareKVRecovery(nil, permissionSnapshot{}, valid)
	require.ErrorIs(t, err, ErrRecoveryConflict)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = PrepareKVRecovery(ctx, permissionSnapshot{}, valid)
	require.ErrorIs(t, err, context.Canceled)
	_, err = CaptureSQLRecovery(nil, nil, nil)
	require.ErrorIs(t, err, ErrRecoveryConflict)
}

func TestRevisionRecoveryRefusesUnsupportedSchemas(t *testing.T) {
	for _, data := range []string{
		`{"version":1,"authority":{"recovery_id":"accepted","epoch":"fresh"}}`,
		`{"version":1,"expected_sha256":"","authority":{"recovery_id":"accepted","epoch":"fresh"},"unknown":true}`,
		`{"version":1,"expected_sha256":"","authority":{"recovery_id":"accepted","epoch":"fresh","unknown":true}}`,
		`{"version":1,"version":1,"expected_sha256":"","authority":{"recovery_id":"accepted","epoch":"fresh"}}`,
		`{"version":2,"expected_sha256":"","authority":{"recovery_id":"accepted","epoch":"fresh"}}`,
		strings.Repeat("x", maxRecoveryTransitionBytes+1),
	} {
		var transition KVRecoveryTransition
		require.Error(t, json.Unmarshal([]byte(data), &transition))
	}
	for _, data := range []string{
		`{"version":1,"authority":{"recovery_id":"accepted","epoch":"fresh"}}`,
		`{"version":1,"expected":null,"authority":{"recovery_id":"accepted","epoch":"fresh"},"unknown":true}`,
		`{"version":1,"expected":{"epoch":"old","sequence":1,"unknown":true},"authority":{"recovery_id":"accepted","epoch":"fresh"}}`,
		`{"version":1,"expected":null,"expected":null,"authority":{"recovery_id":"accepted","epoch":"fresh"}}`,
		`{"version":2,"expected":null,"authority":{"recovery_id":"accepted","epoch":"fresh"}}`,
		strings.Repeat("x", maxRecoveryTransitionBytes+1),
	} {
		var transition SQLRecoveryTransition
		require.Error(t, json.Unmarshal([]byte(data), &transition))
	}
}
