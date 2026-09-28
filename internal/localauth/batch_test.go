package localauth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBatchSessionReceipt(t *testing.T) {
	token := testToken(t, 1)
	gate := NewGate(token, "127.0.0.1")
	now := time.Now().UTC().Truncate(time.Millisecond)
	cookie, original, err := IssueSession(token, GrantLocalToken, now)
	require.NoError(t, err)
	receipt, err := gate.RetainBatchSession(cookie, "account", "batch", now)
	require.NoError(t, err)
	require.NotEqual(t, cookie, receipt)
	restored, err := NewGate(token, "127.0.0.1").VerifyBatchSession(receipt, "account", "batch", now)
	require.NoError(t, err)
	require.Equal(t, original, restored)
	_, err = gate.Verify(receipt, now)
	require.ErrorIs(t, err, ErrBadSignature)
	_, err = gate.VerifyBatchSession(cookie, "account", "batch", now)
	require.ErrorIs(t, err, ErrBadSignature)
	_, err = gate.VerifyBatchSession(receipt, "another", "batch", now)
	require.ErrorIs(t, err, ErrBadSignature)
	_, err = gate.VerifyBatchSession(receipt, "account", "another", now)
	require.ErrorIs(t, err, ErrBadSignature)
	_, err = gate.VerifyBatchSession(receipt, "account", "batch", original.ExpiresAt)
	require.ErrorIs(t, err, ErrSessionExpired)
	_, err = NewGate(testToken(t, 2), "127.0.0.1").VerifyBatchSession(receipt, "account", "batch", now)
	require.ErrorIs(t, err, ErrBadSignature)
	_, err = gate.RetainBatchSession(cookie, "account", "batch", original.ExpiresAt)
	require.ErrorIs(t, err, ErrSessionExpired)
}
