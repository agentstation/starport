package keyring

import (
	"io"
	"net"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestManagedMaterialValkeyOutageAndRecovery(t *testing.T) {
	endpoint := os.Getenv("TEST_VALKEY_URL")
	if endpoint == "" {
		t.Skip("UNVERIFIED: TEST_VALKEY_URL is not set")
	}
	parsed, err := url.Parse(endpoint)
	require.NoError(t, err)
	proxy := newCredentialStorageProxy(t, parsed.Host)
	store, err := storage.OpenValkey(storage.ValkeyConfig{URL: "redis://" + proxy.listener.Addr().String()})
	require.NoError(t, err)
	defer store.Close()
	repo, err := credentials.Open(store)
	require.NoError(t, err)
	provider := syntheticCredentialProvider()
	validator, err := NewCatalogCredentialValidator(func(id catalogs.ProviderID) (catalogs.Provider, bool) { return provider, id == provider.ID })
	require.NoError(t, err)
	master, err := credentials.GenerateMasterKey()
	require.NoError(t, err)
	keys, err := NewProviderKeys(repo, master, validator)
	require.NoError(t, err)
	manager := keys.(*keyManager)
	defer manager.materials.close()
	now := time.Now()
	manager.materials.now = func() time.Time { return now }
	scope := AccountScope(uuid.New().String())
	_, err = keys.AddKey(t.Context(), scope, string(provider.ID), map[string]string{"api-key": "fixture-outage-secret"}, nil, false, 0)
	require.NoError(t, err)
	defer func() { proxy.setOffline(false); _ = keys.DeleteKey(t.Context(), scope, string(provider.ID)) }()
	original, err := keys.ResolveStoredMaterial(t.Context(), scope, provider)
	require.NoError(t, err)
	proxy.setOffline(true)
	warm, err := keys.ResolveStoredMaterial(t.Context(), scope, provider)
	require.NoError(t, err)
	require.NoError(t, warm.CheckValidity(now))
	now = now.Add(manager.materials.limits.Validity)
	_, err = keys.ResolveStoredMaterial(t.Context(), scope, provider)
	require.Error(t, err)
	require.Error(t, original.CheckValidity(now))
	proxy.setOffline(false)
	require.Eventually(t, func() bool {
		material, err := keys.ResolveStoredMaterial(t.Context(), scope, provider)
		if err != nil {
			return false
		}
		value, ok := material.Value("api-key")
		return ok && value == "fixture-outage-secret" && material.CheckValidity(now) == nil
	}, 5*time.Second, 20*time.Millisecond)
	require.Error(t, original.CheckValidity(now))
}

type credentialStorageProxy struct {
	listener    net.Listener
	target      string
	mu          sync.Mutex
	offline     bool
	connections map[net.Conn]struct{}
	work        sync.WaitGroup
}

func newCredentialStorageProxy(t *testing.T, target string) *credentialStorageProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &credentialStorageProxy{listener: listener, target: target, connections: make(map[net.Conn]struct{})}
	p.work.Go(func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			if p.offline {
				p.mu.Unlock()
				_ = client.Close()
				continue
			}
			p.connections[client] = struct{}{}
			p.mu.Unlock()
			p.work.Go(func() { p.forward(client) })
		}
	})
	t.Cleanup(func() { _ = listener.Close(); p.setOffline(true); p.work.Wait() })
	return p
}

func (p *credentialStorageProxy) forward(client net.Conn) {
	defer func() { _ = client.Close(); p.mu.Lock(); delete(p.connections, client); p.mu.Unlock() }()
	upstream, err := net.DialTimeout("tcp", p.target, time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
	var copies sync.WaitGroup
	copies.Go(func() { _, _ = io.Copy(upstream, client); _ = upstream.Close() })
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	copies.Wait()
}

func (p *credentialStorageProxy) setOffline(offline bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.offline = offline
	if offline {
		for connection := range p.connections {
			_ = connection.Close()
		}
	}
}
