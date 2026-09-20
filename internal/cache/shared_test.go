package cache

import (
	"context"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

func TestSharedCacheUnavailableLifecycle(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	store, err := OpenShared("valkey://"+listener.Addr().String(), "unavailable", false)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
		_ = listener.Close()
		<-done
		select {
		case c := <-accepted:
			_ = c.Close()
		default:
		}
	})
	config := ManagerConfig{}
	config.Responses.Strategy = "distributed"
	manager, err := NewCacheManager(config, store)
	require.NoError(t, err)
	value, found, err := manager.GetResponse(t.Context(), "missing")
	require.Error(t, err)
	require.False(t, found)
	require.Nil(t, value)
	require.True(t, manager.FillStatus().Shared.Configured)
	require.False(t, manager.FillStatus().Shared.Available)
	require.NoError(t, manager.SetResponse(t.Context(), "key", []byte("answer")))
	select {
	case conn := <-accepted:
		t.Cleanup(func() { _ = conn.Close() })
	case <-time.After(2 * time.Second):
		t.Fatal("connection owner did not attempt the silent endpoint")
	}
	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cache connection shutdown exceeded its bound")
	}
}

func TestSharedCacheRealService(t *testing.T) {
	raw := os.Getenv("TEST_SHARED_CACHE_URL")
	if raw == "" {
		t.Skip("UNVERIFIED: TEST_SHARED_CACHE_URL is not set")
	}
	options, err := valkey.ParseURL(raw)
	require.NoError(t, err)
	options.DisableCache = true
	options.ForceSingleClient = true
	options.PipelineMultiplex = -1
	control, err := valkey.NewClient(options)
	require.NoError(t, err)
	t.Cleanup(control.Close)
	namespace := "test-" + strings.ReplaceAll(t.Name(), "/", "-")
	first, err := OpenShared(raw, namespace, true)
	require.NoError(t, err)
	second, err := OpenShared(raw, namespace, true)
	require.NoError(t, err)
	isolated, err := OpenShared(raw, namespace+"-other", true)
	require.NoError(t, err)
	for _, store := range []*SharedStore{first, second, isolated} {
		t.Cleanup(func() { require.NoError(t, store.Close()) })
		require.Eventually(t, func() bool { return store.SharedStatus().Available }, 5*time.Second, time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, first.Set(ctx, "entry", []byte("answer"), time.Minute))
	value, found, err := second.Get(ctx, "entry")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "answer", string(value))
	_, found, err = isolated.Get(ctx, "entry")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, first.Set(ctx, "expiry", []byte("expired"), 10*time.Millisecond))
	require.Eventually(t, func() bool { _, found, err := second.Get(ctx, "expiry"); return err == nil && !found }, time.Second, time.Millisecond)
	require.NoError(t, control.Do(ctx, control.B().Set().Key(first.prefix+"oversized").Value(strings.Repeat("x", (1<<20)+1)).Px(time.Minute).Build()).Error())
	value, found, err = second.Get(ctx, "oversized")
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, value)
	connectionID, err := second.current().Do(ctx, second.current().B().ClientId().Build()).AsInt64()
	require.NoError(t, err)
	killed, err := control.Do(ctx, control.B().Arbitrary("CLIENT", "KILL", "ID", strconv.FormatInt(connectionID, 10)).Build()).AsInt64()
	require.NoError(t, err)
	require.Positive(t, killed)
	require.Eventually(t, func() bool {
		value, found, err := second.Get(ctx, "entry")
		return err == nil && found && string(value) == "answer"
	}, time.Second, time.Millisecond)
	require.NoError(t, control.Do(ctx, control.B().Del().Key(first.prefix+"entry", first.prefix+"oversized").Build()).Error())
}

func TestSharedCacheURIAuthenticationAndDatabase(t *testing.T) {
	raw := os.Getenv("TEST_SHARED_CACHE_URL")
	if raw == "" {
		t.Skip("UNVERIFIED: TEST_SHARED_CACHE_URL is not set")
	}
	options, err := valkey.ParseURL(raw)
	require.NoError(t, err)
	options.DisableCache = true
	options.ForceSingleClient = true
	options.PipelineMultiplex = -1
	control, err := valkey.NewClient(options)
	require.NoError(t, err)
	t.Cleanup(control.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, control.Do(ctx, control.B().Arbitrary("ACL", "SETUSER", "cachefixture", "on", ">fixture-password", "~starport:cache:v1:auth-test:*", "+@all").Build()).Error())
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = control.Do(cleanup, control.B().Arbitrary("ACL", "DELUSER", "cachefixture").Build()).Error()
	})
	u, err := url.Parse(raw)
	require.NoError(t, err)
	u.User = url.UserPassword("cachefixture", "fixture-password")
	u.Path = "/3"
	store, err := OpenShared(u.String(), "auth-test", true)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.Eventually(t, func() bool { return store.SharedStatus().Available }, 5*time.Second, time.Millisecond)
	require.NoError(t, store.Set(ctx, "entry", []byte("authenticated"), time.Second))
	got, found, err := store.Get(ctx, "entry")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "authenticated", string(got))
	missing := control.Do(ctx, control.B().Get().Key(store.prefix+"entry").Build()).Error()
	require.True(t, valkey.IsValkeyNil(missing), "selected database must not write to database zero")
}
