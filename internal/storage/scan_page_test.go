package storage_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestScanPagesPreserveEveryKeyAndLiteralPrefix(t *testing.T) {
	repotest.Run(t, func(t *testing.T, store storage.KVStore) {
		prefix := "recover:[*?]\\:"
		for i := range 73 {
			require.NoError(t, store.Set(t.Context(), fmt.Sprintf("%s%03d", prefix, i), []byte("value")))
			require.NoError(t, store.Set(t.Context(), fmt.Sprintf("other:%03d", i), []byte("value")))
		}
		seen := map[string]bool{}
		cursor := ""
		complete := false
		// Valkey scans the whole database before filtering this namespace.
		// Bound elapsed time without assuming a fixed number of cursor steps.
		ctx, cancelScan := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancelScan()
		for {
			page, err := store.ScanPage(ctx, prefix, cursor, 7)
			require.NoError(t, err)
			for _, key := range page.Keys {
				require.True(t, strings.HasPrefix(key, prefix), key)
				seen[key] = true
			}
			cursor = page.Next
			if cursor == "" {
				complete = true
				break
			}
		}
		require.True(t, complete)
		require.Len(t, seen, 73)
		_, err := store.ScanPage(t.Context(), prefix, "", 0)
		require.ErrorIs(t, err, storage.ErrInvalidScan)
		_, err = store.ScanPage(t.Context(), prefix, "invalid cursor!", 7)
		require.ErrorIs(t, err, storage.ErrInvalidScan)
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		_, err = store.ScanPage(canceled, prefix, "", 7)
		require.ErrorIs(t, err, context.Canceled)
	})
}
