package storage

import (
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValkeyURLDatabaseSelection(t *testing.T) {
	raw := os.Getenv("TEST_VALKEY_URL")
	if raw == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is required")
	}
	u, err := url.Parse(raw)
	require.NoError(t, err)
	u.Path = "/1"
	selected, err := OpenValkey(ValkeyConfig{URL: u.String()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, selected.Close()) })
	baseline, err := OpenValkey(ValkeyConfig{URL: raw})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, baseline.Close()) })
	key := "endpoint-database-selection"
	require.NoError(t, selected.Set(t.Context(), key, []byte("selected")))
	defer func() { require.NoError(t, selected.Delete(t.Context(), key)) }()
	value, err := selected.Get(t.Context(), key)
	require.NoError(t, err)
	require.Equal(t, []byte("selected"), value)
	_, err = baseline.Get(t.Context(), key)
	require.ErrorIs(t, err, ErrNotFound)
}
