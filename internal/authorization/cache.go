package authorization

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrCapacity reports a bounded working set or cold-load limit.
var ErrCapacity = errors.New("authorization working set capacity reached")

// Source loads coherent durable records. Errors never represent confirmed absence.
// The source must honor cancellation and verify the evidence's authority and sequence.
type Source interface {
	Load(context.Context, Identity) (Candidate, error)
}

// CacheLimits bounds resident records and concurrent cold loads.
// Bytes measures encoded policy bytes, not total Go heap consumption.
type CacheLimits struct {
	Entries            int
	Bytes              int
	BundleBytes        int
	ConcurrentLoads    int
	TenantLoads        int
	LoadTimeout        time.Duration
	PermissionLifetime time.Duration
	ClockUncertainty   time.Duration
}

// Clock supplies current time and the deployment's clock-health decision.
// It must support concurrent calls without blocking or storage reads.
type Clock func() (time.Time, bool)

type cacheEntry struct {
	bundle *Bundle
	flight *flight
	used   uint64
}

type flight struct {
	done   chan struct{}
	bundle *Bundle
	err    error
}

// Cache owns a bounded active-caller working set. NewCache does not read storage.
type Cache struct {
	mu          sync.Mutex
	source      Source
	authorities *AuthoritySet
	limits      CacheLimits
	clock       Clock
	ctx         context.Context
	cancel      context.CancelFunc
	entries     map[Identity]*cacheEntry
	tenants     map[string]int
	bytes       int
	resident    int
	active      int
	access      uint64
	closed      bool
	work        sync.WaitGroup
}

// NewCache requires explicit limits and a clock-health provider.
func NewCache(source Source, authorities *AuthoritySet, limits CacheLimits, clock Clock) (*Cache, error) {
	if source == nil || authorities == nil || len(authorities.fences) == 0 || clock == nil || limits.Entries <= 0 || limits.Bytes <= 0 || limits.BundleBytes <= 0 || limits.BundleBytes > limits.Bytes || limits.ConcurrentLoads <= 0 || limits.TenantLoads <= 0 || limits.TenantLoads > limits.ConcurrentLoads || limits.LoadTimeout <= 0 || limits.PermissionLifetime <= 0 || limits.ClockUncertainty < 0 || limits.ClockUncertainty >= limits.PermissionLifetime {
		return nil, ErrEvidence
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Cache{source: source, authorities: authorities, limits: limits, clock: clock, ctx: ctx, cancel: cancel, entries: make(map[Identity]*cacheEntry), tenants: make(map[string]int)}, nil
}

// Resolve returns a valid memory bundle or joins one bounded cold load.
// A caller's cancellation does not cancel work that other callers share.
func (c *Cache) Resolve(ctx context.Context, identity Identity) (*Bundle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if identity.Subject == "" || len(identity.Subject) > 256 || len(identity.Tenant) > 255 {
		return nil, ErrEvidence
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrUnavailable
	}
	now, healthy := c.clock()
	if !healthy {
		c.mu.Unlock()
		return nil, ErrUnavailable
	}
	entry := c.entries[identity]
	if entry != nil {
		if entry.bundle != nil && entry.bundle.receipt.Check(now, healthy) == nil {
			c.access++
			entry.used = c.access
			result := entry.bundle
			c.mu.Unlock()
			return result, nil
		}
		if entry.flight != nil {
			pending := entry.flight
			c.mu.Unlock()
			return c.wait(ctx, pending)
		}
		c.remove(identity, entry)
	}
	if c.active >= c.limits.ConcurrentLoads || c.tenants[identity.Tenant] >= c.limits.TenantLoads {
		c.mu.Unlock()
		return nil, ErrCapacity
	}
	ticket, err := c.authorities.start()
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	pending := &flight{done: make(chan struct{})}
	entry = &cacheEntry{flight: pending}
	c.entries[identity] = entry
	c.active++
	c.tenants[identity.Tenant]++
	c.work.Add(1)
	c.mu.Unlock()
	go c.load(identity, entry, pending, ticket)
	return c.wait(ctx, pending)
}

func (c *Cache) wait(ctx context.Context, pending *flight) (*Bundle, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-pending.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pending.err != nil {
			return nil, pending.err
		}
		now, healthy := c.clock()
		if err := pending.bundle.receipt.Check(now, healthy); err != nil {
			return nil, err
		}
		return pending.bundle, nil
	}
}

func (c *Cache) load(identity Identity, entry *cacheEntry, pending *flight, ticket tickets) {
	defer c.work.Done()
	ctx, cancel := context.WithTimeout(c.ctx, c.limits.LoadTimeout)
	defer cancel()
	bundle, err := c.loadBundle(ctx, identity, ticket)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active--
	c.tenants[identity.Tenant]--
	if c.tenants[identity.Tenant] == 0 {
		delete(c.tenants, identity.Tenant)
	}
	if c.closed {
		err = ErrUnavailable
	}
	if err == nil {
		now, healthy := c.clock()
		err = bundle.receipt.Check(now, healthy)
	}
	if err == nil {
		for c.resident >= c.limits.Entries || c.bytes+bundle.bytes > c.limits.Bytes {
			if !c.evict() {
				err = ErrCapacity
				break
			}
		}
	}
	if err == nil {
		c.access++
		entry.bundle, entry.flight, entry.used = bundle, nil, c.access
		c.bytes += bundle.bytes
		c.resident++
		pending.bundle = bundle
	} else {
		delete(c.entries, identity)
		pending.err = err
	}
	close(pending.done)
}

// loadBundle retries a changed authority at most twice within the original deadline.
// Every retry captures new tickets before rereading every source dependency.
func (c *Cache) loadBundle(ctx context.Context, identity Identity, ticket tickets) (*Bundle, error) {
	for attempt := range 3 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if attempt > 0 {
			var err error
			ticket, err = c.authorities.start()
			if err != nil {
				return nil, err
			}
		}
		bundle, err := c.loadOnce(ctx, identity, ticket)
		if !errors.Is(err, ErrWithdrawn) {
			return bundle, err
		}
	}
	return nil, ErrWithdrawn
}

func (c *Cache) loadOnce(ctx context.Context, identity Identity, ticket tickets) (*Bundle, error) {
	candidate, err := c.source.Load(ctx, identity)
	if err == nil {
		err = ctx.Err()
	}
	var bundle *Bundle
	if err == nil {
		now, healthy := c.clock()
		var receipt Permit
		receipt, err = ticket.accept(candidate.Evidence, now, c.limits.PermissionLifetime, c.limits.ClockUncertainty, healthy)
		if err == nil && candidate.Key.APIKey.ExpiresAt != nil {
			deadline := candidate.Key.APIKey.ExpiresAt.Add(-c.limits.ClockUncertainty)
			receipt.clamp(deadline)
			err = receipt.Check(now, healthy)
		}
		if err == nil {
			bundle, err = freeze(candidate, identity, receipt, c.limits.BundleBytes)
		}
	}
	return bundle, err
}

func (c *Cache) remove(identity Identity, entry *cacheEntry) {
	if entry.bundle != nil {
		c.bytes -= entry.bundle.bytes
		c.resident--
	}
	delete(c.entries, identity)
}

func (c *Cache) evict() bool {
	var oldest Identity
	var victim *cacheEntry
	for key, entry := range c.entries {
		if entry.flight == nil && (victim == nil || entry.used < victim.used) {
			oldest, victim = key, entry
		}
	}
	if victim == nil {
		return false
	}
	c.remove(oldest, victim)
	return true
}

// Close cancels owned loads, revokes outstanding receipts, and waits for workers.
func (c *Cache) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.work.Wait()
		return
	}
	c.closed = true
	finish := c.authorities.beginMutation()
	clear(c.entries)
	c.bytes = 0
	c.resident = 0
	c.cancel()
	c.mu.Unlock()
	c.work.Wait()
	finish()
}
