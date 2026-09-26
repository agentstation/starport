package deployment

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKeyPrefixOpaqueIdentity(t *testing.T) {
	seen := map[string]bool{}
	for _, id := range []string{"local", "a:b", "a/b", "東京:*?[]", "é", "é", strings.Repeat("a", 256)} {
		prefix, err := KeyPrefix(id)
		require.NoError(t, err)
		require.False(t, seen[prefix])
		seen[prefix] = true
		encoded := strings.TrimSuffix(strings.TrimPrefix(prefix, "starport:v1:"), ":")
		require.NotContains(t, encoded, ":")
		require.False(t, strings.ContainsAny(encoded, "*?[]\\"))
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		require.NoError(t, err)
		require.Equal(t, id, string(decoded))
	}
}

func TestKeyPrefixRefusesInvalidIdentity(t *testing.T) {
	for _, id := range []string{"", " leading", "trailing ", "line\nfeed", string([]byte{255}), strings.Repeat("a", 257)} {
		prefix, err := KeyPrefix(id)
		require.Error(t, err)
		require.Empty(t, prefix)
	}
}
