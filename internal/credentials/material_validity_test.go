package credentials

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMaterialRenewalDoesNotExtendExistingHandle(t *testing.T) {
	now := time.Now()
	first := NewMaterialValidity(now.Add(time.Second))
	renewed := first.Renew(now.Add(2 * time.Second))
	require.ErrorIs(t, first.Check(now.Add(time.Second)), ErrMaterialExpired)
	require.NoError(t, renewed.Check(now.Add(time.Second)))
	renewed.Revoke()
	require.ErrorIs(t, first.Check(now), ErrMaterialRevoked)
	require.ErrorIs(t, renewed.Check(now), ErrMaterialRevoked)
}

func TestZeroMaterialValidityRefusesUse(t *testing.T) {
	var validity MaterialValidity
	require.ErrorIs(t, validity.Check(time.Now()), ErrMaterialRevoked)
	validity.Revoke()
	require.ErrorIs(t, validity.Renew(time.Now().Add(time.Minute)).Check(time.Now()), ErrMaterialRevoked)
}

func TestMaterialWithoutExpiryRemainsRevocable(t *testing.T) {
	validity := NewMaterialValidity(time.Time{})
	now := time.Now()
	require.NoError(t, validity.Check(now))
	validity.Revoke()
	require.ErrorIs(t, validity.Check(now), ErrMaterialRevoked)
}
