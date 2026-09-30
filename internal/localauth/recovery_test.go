package localauth

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRecoveryTokenChecksOwnerScopeAndExposure(t *testing.T) {
	token, err := Mint(1, time.Now())
	require.NoError(t, err)
	body, err := json.Marshal(token)
	require.NoError(t, err)
	require.NoError(t, CheckRecoveryToken(body, "127.0.0.1"))
	require.Error(t, CheckRecoveryToken(body, "0.0.0.0"))
	now := time.Now()
	token.RotatedAt = &now
	body, err = json.Marshal(token)
	require.NoError(t, err)
	require.NoError(t, CheckRecoveryToken(body, "0.0.0.0"))
	token.Scope = "gateway-api-key"
	body, err = json.Marshal(token)
	require.NoError(t, err)
	require.Error(t, CheckRecoveryToken(body, "127.0.0.1"))
	require.Error(t, CheckRecoveryToken([]byte("invalid private credential"), "127.0.0.1"))
}
