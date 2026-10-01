package storage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// valkeyInfo reads one INFO section from the real service as name-value pairs.
func valkeyInfo(t *testing.T, store *ValkeyStore, section string) map[string]string {
	t.Helper()
	raw, err := store.client.Do(t.Context(), store.client.B().Info().Section(section).Build()).ToString()
	require.NoError(t, err)
	fields := map[string]string{}
	for line := range strings.SplitSeq(raw, "\n") {
		if name, value, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
			fields[name] = value
		}
	}
	return fields
}

// TestQualifiedValkeyVersion proves that the real test service is the qualified release in the qualified mode.
// A different release fails here until a qualification run records it in QualifiedValkeyVersion.
func TestQualifiedValkeyVersion(t *testing.T) {
	store := incarnationTestStore(t, "TEST_VALKEY_URL")
	server := valkeyInfo(t, store, "server")
	require.Equal(t, QualifiedValkeyVersion, server["valkey_version"], "the service is not the qualified Valkey release")
	require.Equal(t, "master", valkeyInfo(t, store, "replication")["role"], "the qualified mode is one writable primary")
	require.Equal(t, "0", valkeyInfo(t, store, "cluster")["cluster_enabled"], "Cluster mode is not qualified")
}
