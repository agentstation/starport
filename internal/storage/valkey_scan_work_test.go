package storage

import (
	"context"
	"crypto/rand"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

type scanCountingClient struct {
	valkey.Client
	scans int
}

func (c *scanCountingClient) Do(ctx context.Context, command valkey.Completed) valkey.ValkeyResult {
	if command.Commands()[0] == "SCAN" {
		c.scans++
	}
	return c.Client.Do(ctx, command)
}

func TestValkeySmallResultLimitBoundsScanRoundTrips(t *testing.T) {
	endpoint := os.Getenv("TEST_VALKEY_URL")
	if endpoint == "" {
		t.Skip("TEST_VALKEY_URL is not configured")
	}
	backend, err := OpenValkey(ValkeyConfig{URL: endpoint, DB: 14, DeploymentID: "scan-work-" + rand.Text()})
	require.NoError(t, err)
	defer backend.Close()
	store := backend.(*ValkeyStore)
	counter := &scanCountingClient{Client: store.client}
	store.client = counter
	keys := make([]string, 4096)
	command := store.client.B().Mset().KeyValue()
	for i := range keys {
		keys[i] = store.prefix + rand.Text()
		command = command.KeyValue(keys[i], "test")
	}
	require.NoError(t, store.client.Do(t.Context(), command.Build()).Error())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, store.client.Do(ctx, store.client.B().Del().Key(keys...).Build()).Error())
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	found, err := store.ScanWithPrefix(ctx, "absent:", 1)
	t.Logf("empty scan used %d requests", counter.scans)
	require.NoError(t, err)
	require.Empty(t, found)
	require.LessOrEqual(t, counter.scans, 64, "a result limit must not force one scan step per key")
	found, err = store.Scan(ctx, "*", 1)
	require.NoError(t, err)
	require.Len(t, found, 1)
}
