package keyring

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/credentials"
)

// ErrMaterialCapacity reports bounded credential work or memory exhaustion.
var ErrMaterialCapacity = errors.New("managed credential capacity reached")

// ErrMaterialClosed reports an application-owned cache that stopped.
var ErrMaterialClosed = errors.New("managed credential material stopped")

// ErrMaterialChanged rejects a load invalidated by a concurrent mutation.
var ErrMaterialChanged = errors.New("managed credential changed during resolution")

const (
	managedMaterialEntries     = 1024
	managedMaterialBytes       = 16 << 20
	managedMaterialLoads       = 4
	managedMaterialTenantLoads = 2
	managedMaterialValidity    = 5 * time.Second
	managedMaterialLoadTimeout = time.Second
)

type materialIdentity struct{ scope, provider, account string }

type managedEntry struct {
	provider  catalogs.Provider
	material  credentials.Material
	deadline  time.Time
	refreshAt time.Time
	lastUsed  time.Time
	busy      chan struct{}
	bytes     int
}

type managedMaterials struct {
	mu      sync.Mutex
	closed  bool
	entries map[materialIdentity]*managedEntry
	tenants map[string]int
	active  int
	bytes   int
	now     func() time.Time
}

func newManagedMaterials() *managedMaterials {
	return &managedMaterials{entries: make(map[materialIdentity]*managedEntry), tenants: make(map[string]int), now: time.Now}
}

func (c *managedMaterials) invalidate(scope, provider string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if key.scope == scope && key.provider == provider {
			c.remove(key, entry)
		}
	}
}

func (c *managedMaterials) remove(key materialIdentity, entry *managedEntry) {
	delete(c.entries, key)
	c.bytes -= entry.bytes
}

func (c *managedMaterials) resolve(ctx context.Context, key materialIdentity, provider catalogs.Provider, force bool, load func(context.Context) (credentials.Material, error)) (credentials.Material, error) {
	for {
		if err := ctx.Err(); err != nil {
			return credentials.Material{}, err
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return credentials.Material{}, ErrMaterialClosed
		}
		entry := c.entries[key]
		if force && (entry == nil || !reflect.DeepEqual(entry.provider.Credentials, provider.Credentials)) {
			c.mu.Unlock()
			return credentials.Material{}, ErrMaterialChanged
		}
		if entry != nil && !reflect.DeepEqual(entry.provider.Credentials, provider.Credentials) {
			c.remove(key, entry)
			entry = nil
		}
		if !force && entry != nil && c.now().Before(entry.deadline) {
			entry.lastUsed = c.now()
			result := entry.material
			c.mu.Unlock()
			return result, nil
		}
		if entry != nil && entry.busy != nil {
			done := entry.busy
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return credentials.Material{}, ctx.Err()
			case <-done:
				continue
			}
		}
		if c.active >= managedMaterialLoads || c.tenants[key.scope+"\x00"+key.account] >= managedMaterialTenantLoads {
			c.mu.Unlock()
			return credentials.Material{}, ErrMaterialCapacity
		}
		if entry == nil {
			if len(c.entries) >= managedMaterialEntries {
				for oldKey, oldEntry := range c.entries {
					if oldEntry.busy == nil && !c.now().Before(oldEntry.deadline) {
						c.remove(oldKey, oldEntry)
					}
				}
			}
			if len(c.entries) >= managedMaterialEntries {
				c.mu.Unlock()
				return credentials.Material{}, ErrMaterialCapacity
			}
			entry = &managedEntry{lastUsed: c.now(), provider: catalogs.DeepCopyProvider(catalogs.Provider{ID: provider.ID, Credentials: provider.Credentials})}
			c.entries[key] = entry
		}
		entry.busy = make(chan struct{})
		done := entry.busy
		c.active++
		c.tenants[key.scope+"\x00"+key.account]++
		started := c.now()
		c.mu.Unlock()
		loadCtx, cancel := context.WithTimeout(ctx, managedMaterialLoadTimeout)
		material, err := load(loadCtx)
		if err == nil {
			err = loadCtx.Err()
		}
		cancel()
		c.mu.Lock()
		c.active--
		c.tenants[key.scope+"\x00"+key.account]--
		if c.tenants[key.scope+"\x00"+key.account] == 0 {
			delete(c.tenants, key.scope+"\x00"+key.account)
		}
		err = c.publishLoaded(key, entry, material, started, err)
		entry.busy = nil
		close(done)
		c.mu.Unlock()
		if err != nil {
			return credentials.Material{}, err
		}
		return material, nil
	}
}

// publishLoaded runs with the cache mutex held.
func (c *managedMaterials) publishLoaded(key materialIdentity, entry *managedEntry, material credentials.Material, started time.Time, err error) error {
	if c.entries[key] != entry {
		err = ErrMaterialChanged
	} else if err != nil {
		c.remove(key, entry)
	} else {
		size := material.SecretBytes()
		if expiry, ok := material.ExpiresAt(); ok && !c.now().Before(expiry) {
			err = credentials.NewSourceError(credentials.SourceErrorInvalid, "stored")
		}
		if err != nil {
			c.remove(key, entry)
		} else if c.bytes-entry.bytes+size > managedMaterialBytes || !c.now().Before(started.Add(managedMaterialValidity)) {
			c.remove(key, entry)
			err = ErrMaterialCapacity
		} else {
			c.bytes += size - entry.bytes
			entry.bytes = size
			entry.material = material
			entry.deadline = started.Add(managedMaterialValidity)
			if expiry, ok := material.ExpiresAt(); ok && expiry.Before(entry.deadline) {
				entry.deadline = expiry
			}
			entry.refreshAt = started.Add(managedMaterialValidity / 2)
		}
	}
	return err
}

// ManagedProviderKeys owns stored credential operations and refresh work.
type ManagedProviderKeys interface {
	ProviderKeys
	RunMaterialRefresh(context.Context) error
}

// RunMaterialRefresh renews active entries until the application stops it.
func (m *keyManager) RunMaterialRefresh(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer m.materials.close()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.refreshMaterials(ctx)
		}
	}
}

func (m *keyManager) refreshMaterials(ctx context.Context) {
	type refresh struct {
		key      materialIdentity
		provider catalogs.Provider
	}
	c := m.materials
	c.mu.Lock()
	pending := make([]refresh, 0, managedMaterialLoads)
	for key, entry := range c.entries {
		if entry.busy == nil && c.now().Sub(entry.lastUsed) >= time.Minute {
			c.remove(key, entry)
			continue
		}
		if entry.busy == nil && !c.now().Before(entry.refreshAt) {
			pending = append(pending, refresh{key: key, provider: entry.provider})
			if len(pending) == managedMaterialLoads {
				break
			}
		}
	}
	c.mu.Unlock()
	var wait sync.WaitGroup
	for _, item := range pending {
		wait.Go(func() {
			_, _ = c.resolve(ctx, item.key, item.provider, true, func(ctx context.Context) (credentials.Material, error) {
				if item.key.scope == SharedScope {
					return m.loadSharedMaterial(ctx, item.key.account, item.provider)
				}
				return m.loadStoredMaterial(ctx, item.key.scope, item.provider)
			})
		})
	}
	wait.Wait()
}

func (c *managedMaterials) previous(key materialIdentity, provider catalogs.Provider, version string) (credentials.Material, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil || entry.material.Empty() || entry.material.Version() != version || !reflect.DeepEqual(entry.provider.Credentials, provider.Credentials) {
		return credentials.Material{}, false
	}
	return entry.material, true
}

func (c *managedMaterials) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	clear(c.entries)
	c.bytes = 0
}
