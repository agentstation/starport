package repotest

import (
	"context"
	"testing"

	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestNamespacedStoreIsolatesContractKeys(t *testing.T) {
	ctx := context.Background()
	raw := storage.NewMockStore()
	t.Cleanup(func() { _ = raw.Close() })
	require.NoError(t, raw.Set(ctx, "records:foreign", []byte("foreign")))

	scoped := newNamespacedStore(t, raw)
	require.NoError(t, scoped.Set(ctx, "records:owned", []byte("owned")))
	keys, err := scoped.ScanWithPrefix(ctx, "records:", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"records:owned"}, keys)

	values, err := scoped.BatchGet(ctx, keys)
	require.NoError(t, err)
	require.Equal(t, []byte("owned"), values["records:owned"])
	require.NotContains(t, values, "records:foreign")
}
