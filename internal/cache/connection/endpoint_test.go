package connection

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEndpointValidation(t *testing.T) {
	for _, raw := range []string{"valkey://[::1]", "valkey://localhost", "valkeys://cache.example/3"} {
		t.Run(raw, func(t *testing.T) {
			u, err := Parse(raw, false)
			require.NoError(t, err)
			require.Equal(t, "6379", u.Port())
		})
	}
	for _, raw := range []string{"valkey://::1", "valkeys://cache.example/-1", "valkeys://cache.example/65536", "valkeys://cache.example:0", "valkeys://cache.example:99999", "valkeys://cache.example/3/4", "valkeys://cache.example?addr=another", "valkeys://cache.example#fragment", "valkeys://", "unix:///tmp/cache.sock"} {
		t.Run(raw, func(t *testing.T) { _, err := Parse(raw, false); require.Error(t, err) })
	}
}
