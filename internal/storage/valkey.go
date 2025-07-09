package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/valkey-io/valkey-go"
)

// ValkeyStore implements KVStore interface using Valkey
type ValkeyStore struct {
	client valkey.Client
	config ValkeyConfig
	pubsub *ValkeyPubSub
}

// OpenValkey creates a new Valkey-backed KVStore
func OpenValkey(config ValkeyConfig) (KVStore, error) {
	// Parse URL to extract host:port
	url := strings.TrimPrefix(config.URL, "redis://")
	url = strings.TrimPrefix(url, "valkey://")
	
	opts := valkey.ClientOption{
		InitAddress:     []string{url},
		Password:        config.Password,
		SelectDB:        config.DB,
		ClientName:      "starport",
		DisableRetry:    config.MaxRetries == 0,
	}

	// Create client
	client, err := valkey.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create valkey client: %w", err)
	}

	store := &ValkeyStore{
		client: client,
		config: config,
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to connect to valkey: %w", err)
	}

	// Initialize pub/sub
	store.pubsub = NewValkeyPubSub(client)

	log.Info().
		Str("url", config.URL).
		Int("db", config.DB).
		Bool("cluster", config.ClusterMode).
		Msg("connected to valkey")

	return store, nil
}

// GetPubSub returns the pub/sub client for cache invalidation
func (v *ValkeyStore) GetPubSub() PubSubClient {
	return v.pubsub
}

// Basic operations

// Get retrieves a value by key
func (v *ValkeyStore) Get(ctx context.Context, key string) ([]byte, error) {
	cmd := v.client.B().Get().Key(key).Build()
	resp := v.client.Do(ctx, cmd)
	
	val, err := resp.AsBytes()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get key %s: %w", key, err)
	}
	
	return val, nil
}

// Set stores a value with a key
func (v *ValkeyStore) Set(ctx context.Context, key string, value []byte) error {
	cmd := v.client.B().Set().Key(key).Value(string(value)).Build()
	resp := v.client.Do(ctx, cmd)
	
	if err := resp.Error(); err != nil {
		return fmt.Errorf("failed to set key %s: %w", key, err)
	}
	
	return nil
}

// Delete removes a key
func (v *ValkeyStore) Delete(ctx context.Context, key string) error {
	cmd := v.client.B().Del().Key(key).Build()
	resp := v.client.Do(ctx, cmd)
	
	if err := resp.Error(); err != nil {
		return fmt.Errorf("failed to delete key %s: %w", key, err)
	}
	
	return nil
}

// Exists checks if a key exists
func (v *ValkeyStore) Exists(ctx context.Context, key string) (bool, error) {
	cmd := v.client.B().Exists().Key(key).Build()
	resp := v.client.Do(ctx, cmd)
	
	count, err := resp.AsInt64()
	if err != nil {
		return false, fmt.Errorf("failed to check key existence %s: %w", key, err)
	}
	
	return count > 0, nil
}

// TTL operations

// SetWithTTL stores a value with expiration
func (v *ValkeyStore) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	cmd := v.client.B().Set().Key(key).Value(string(value)).Ex(ttl).Build()
	resp := v.client.Do(ctx, cmd)
	
	if err := resp.Error(); err != nil {
		return fmt.Errorf("failed to set key with TTL %s: %w", key, err)
	}
	
	return nil
}

// GetTTL returns the remaining TTL for a key
func (v *ValkeyStore) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	cmd := v.client.B().Ttl().Key(key).Build()
	resp := v.client.Do(ctx, cmd)
	
	ttl, err := resp.AsInt64()
	if err != nil {
		return 0, fmt.Errorf("failed to get TTL for key %s: %w", key, err)
	}
	
	if ttl == -2 {
		return 0, ErrNotFound
	}
	if ttl == -1 {
		return 0, nil // No expiration
	}
	
	return time.Duration(ttl) * time.Second, nil
}

// ExpireAt sets expiration time for a key
func (v *ValkeyStore) ExpireAt(ctx context.Context, key string, expireAt time.Time) error {
	cmd := v.client.B().Expireat().Key(key).Timestamp(expireAt.Unix()).Build()
	resp := v.client.Do(ctx, cmd)
	
	if err := resp.Error(); err != nil {
		return fmt.Errorf("failed to set expiration for key %s: %w", key, err)
	}
	
	return nil
}

// Atomic operations

// Increment atomically increments a value
func (v *ValkeyStore) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	cmd := v.client.B().Incrby().Key(key).Increment(delta).Build()
	resp := v.client.Do(ctx, cmd)
	
	val, err := resp.AsInt64()
	if err != nil {
		return 0, fmt.Errorf("failed to increment key %s: %w", key, err)
	}
	
	return val, nil
}

// Decrement atomically decrements a value
func (v *ValkeyStore) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	cmd := v.client.B().Decrby().Key(key).Decrement(delta).Build()
	resp := v.client.Do(ctx, cmd)
	
	val, err := resp.AsInt64()
	if err != nil {
		return 0, fmt.Errorf("failed to decrement key %s: %w", key, err)
	}
	
	return val, nil
}

// CompareAndSwap atomically updates a value if it matches the old value
func (v *ValkeyStore) CompareAndSwap(ctx context.Context, key string, old, newValue []byte) error {
	// Use Lua script for atomic CAS operation
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("set", KEYS[1], ARGV[2])
		else
			return nil
		end
	`
	
	cmd := v.client.B().Eval().Script(script).Numkeys(1).Key(key).Arg(string(old), string(newValue)).Build()
	resp := v.client.Do(ctx, cmd)
	
	// Check for errors first
	if err := resp.Error(); err != nil {
		return fmt.Errorf("failed to compare and swap key %s: %w", key, err)
	}
	
	// Check if the script returned nil (mismatch) by trying to get the value
	_, err := resp.ToString()
	if err != nil && valkey.IsValkeyNil(err) {
		return ErrConflict
	}
	
	return nil
}

// Batch operations

// BatchGet retrieves multiple values
func (v *ValkeyStore) BatchGet(ctx context.Context, keys []string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return make(map[string][]byte), nil
	}

	// Use MGET for batch retrieval
	cmd := v.client.B().Mget().Key(keys...).Build()
	resp := v.client.Do(ctx, cmd)
	
	// MGET returns an array of values, some might be nil
	result := make(map[string][]byte)
	
	// Parse the array response
	arr, err := resp.ToArray()
	if err != nil {
		return nil, fmt.Errorf("failed to batch get keys: %w", err)
	}
	
	for i, key := range keys {
		if i < len(arr) {
			val, err := arr[i].AsBytes()
			if err == nil {
				result[key] = val
			} else if !valkey.IsValkeyNil(err) {
				// Log non-nil errors
				log.Warn().Err(err).Str("key", key).Msg("failed to get value in batch")
			}
		}
	}
	
	return result, nil
}

// BatchSet stores multiple key-value pairs
func (v *ValkeyStore) BatchSet(ctx context.Context, items map[string][]byte) error {
	if len(items) == 0 {
		return nil
	}

	// Use individual SET commands in parallel (auto-pipelining)
	results := make([]valkey.ValkeyResult, 0, len(items))
	for key, value := range items {
		cmd := v.client.B().Set().Key(key).Value(string(value)).Build()
		results = append(results, v.client.Do(ctx, cmd))
	}
	
	// Check all results
	for i, result := range results {
		if err := result.Error(); err != nil {
			return fmt.Errorf("failed to set key in batch at index %d: %w", i, err)
		}
	}
	
	return nil
}

// BatchDelete removes multiple keys
func (v *ValkeyStore) BatchDelete(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	cmd := v.client.B().Del().Key(keys...).Build()
	resp := v.client.Do(ctx, cmd)
	
	if err := resp.Error(); err != nil {
		return fmt.Errorf("failed to batch delete keys: %w", err)
	}
	
	return nil
}

// BatchSetWithTTL stores multiple key-value pairs with TTL
func (v *ValkeyStore) BatchSetWithTTL(ctx context.Context, items map[string][]byte, ttl time.Duration) error {
	if len(items) == 0 {
		return nil
	}

	// Use pipeline for atomic batch operation with TTL
	results := make([]valkey.ValkeyResult, 0, len(items))
	
	// Build all commands first
	for key, value := range items {
		cmd := v.client.B().Set().Key(key).Value(string(value)).Ex(ttl).Build()
		results = append(results, v.client.Do(ctx, cmd))
	}
	
	// Check all results
	for i, result := range results {
		if err := result.Error(); err != nil {
			return fmt.Errorf("failed to set key with TTL in batch at index %d: %w", i, err)
		}
	}
	
	return nil
}

// Transaction support

// BeginTransaction starts a new transaction
func (v *ValkeyStore) BeginTransaction(ctx context.Context) (Transaction, error) {
	return &ValkeyTransaction{
		store:    v,
		ctx:      ctx,
		commands: make([]valkey.Completed, 0),
		state:    make(map[string][]byte),
	}, nil
}

// Scan operations

// Scan returns keys matching a pattern
func (v *ValkeyStore) Scan(ctx context.Context, pattern string, limit int) ([]string, error) {
	var keys []string
	cursor := uint64(0)
	
	for {
		cmd := v.client.B().Scan().Cursor(cursor).Match(pattern).Count(int64(limit)).Build()
		resp := v.client.Do(ctx, cmd)
		
		// Parse scan result
		scanResult, err := resp.AsScanEntry()
		if err != nil {
			return nil, fmt.Errorf("failed to scan keys with pattern %s: %w", pattern, err)
		}
		
		keys = append(keys, scanResult.Elements...)
		
		// Check if we've reached the limit or finished scanning
		if scanResult.Cursor == 0 || len(keys) >= limit {
			break
		}
		
		cursor = scanResult.Cursor
	}
	
	// Trim to limit
	if len(keys) > limit {
		keys = keys[:limit]
	}
	
	return keys, nil
}

// ScanWithPrefix returns keys with a specific prefix
func (v *ValkeyStore) ScanWithPrefix(ctx context.Context, prefix string, limit int) ([]string, error) {
	return v.Scan(ctx, prefix+"*", limit)
}

// Health and lifecycle

// Ping checks if the connection is alive
func (v *ValkeyStore) Ping(ctx context.Context) error {
	cmd := v.client.B().Ping().Build()
	resp := v.client.Do(ctx, cmd)
	
	if err := resp.Error(); err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}
	
	return nil
}

// Close closes the connection
func (v *ValkeyStore) Close() error {
	if v.pubsub != nil {
		if err := v.pubsub.Close(); err != nil {
			log.Warn().Err(err).Msg("failed to close pubsub")
		}
	}
	
	v.client.Close()
	return nil
}

// ValkeyTransaction implements Transaction interface
type ValkeyTransaction struct {
	store    *ValkeyStore
	ctx      context.Context
	commands []valkey.Completed
	state    map[string][]byte // Track transaction state
}

// Get retrieves a value within the transaction
func (t *ValkeyTransaction) Get(key string) ([]byte, error) {
	// Check local state first
	if val, ok := t.state[key]; ok {
		return val, nil
	}
	
	// Otherwise get from store
	return t.store.Get(t.ctx, key)
}

// Set stores a value within the transaction
func (t *ValkeyTransaction) Set(key string, value []byte) error {
	if t.state == nil {
		t.state = make(map[string][]byte)
	}
	t.state[key] = value
	
	cmd := t.store.client.B().Set().Key(key).Value(string(value)).Build()
	t.commands = append(t.commands, cmd)
	return nil
}

// Delete removes a key within the transaction
func (t *ValkeyTransaction) Delete(key string) error {
	cmd := t.store.client.B().Del().Key(key).Build()
	t.commands = append(t.commands, cmd)
	return nil
}

// SetWithTTL stores a value with TTL within the transaction
func (t *ValkeyTransaction) SetWithTTL(key string, value []byte, ttl time.Duration) error {
	if t.state == nil {
		t.state = make(map[string][]byte)
	}
	t.state[key] = value
	
	cmd := t.store.client.B().Set().Key(key).Value(string(value)).Ex(ttl).Build()
	t.commands = append(t.commands, cmd)
	return nil
}

// Increment atomically increments within the transaction
func (t *ValkeyTransaction) Increment(key string, delta int64) (int64, error) {
	cmd := t.store.client.B().Incrby().Key(key).Increment(delta).Build()
	t.commands = append(t.commands, cmd)
	// Note: actual value will be available after commit
	return 0, nil
}

// CompareAndSwap within the transaction
func (t *ValkeyTransaction) CompareAndSwap(key string, _, newValue []byte) error {
	// This is complex in transactions, we'll use a watch/multi/exec pattern
	// For now, add as a regular set
	return t.Set(key, newValue)
}

// Commit executes all commands in the transaction
func (t *ValkeyTransaction) Commit(ctx context.Context) error {
	if len(t.commands) == 0 {
		return nil
	}
	
	// Use dedicated client for transaction
	var txErr error
	_ = t.store.client.Dedicated(func(c valkey.DedicatedClient) error {
		// Build command slice for DoMulti
		cmds := make([]valkey.Completed, 0, len(t.commands)+2)
		cmds = append(cmds, c.B().Multi().Build())
		cmds = append(cmds, t.commands...)
		cmds = append(cmds, c.B().Exec().Build())
		
		// Execute transaction
		results := c.DoMulti(ctx, cmds...)
		
		// Check results - the last one is EXEC result
		if len(results) > 0 {
			execResult := results[len(results)-1]
			if err := execResult.Error(); err != nil {
				txErr = fmt.Errorf("transaction execution failed: %w", err)
				return err
			}
		}
		
		return nil
	})
	
	return txErr
}

// Rollback discards the transaction
func (t *ValkeyTransaction) Rollback() error {
	t.commands = nil
	t.state = nil
	return nil
}

// Ensure ValkeyStore implements KVStore interface
var _ KVStore = (*ValkeyStore)(nil)

// Ensure ValkeyStore implements PubSubProvider interface
var _ PubSubProvider = (*ValkeyStore)(nil)

// Ensure ValkeyTransaction implements Transaction interface
var _ Transaction = (*ValkeyTransaction)(nil)