package storage

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"time"

	"github.com/agentstation/starport/internal/deployment"

	"github.com/rs/zerolog/log"
	"github.com/valkey-io/valkey-go"
)

// ValkeyStore implements KVStore interface using Valkey
type ValkeyStore struct {
	client           valkey.Client
	prefix           string
	config           ValkeyConfig
	pubsub           *ValkeyPubSub
	operationTimeout time.Duration
}

// OpenValkey creates a new Valkey-backed KVStore
func OpenValkey(config ValkeyConfig) (KVStore, error) {
	prefix, err := deployment.KeyPrefix(config.DeploymentID)
	if err != nil {
		return nil, fmt.Errorf("durable KV deployment identity: %w", err)
	}
	return openValkey(config, "{"+prefix+"}kv:")
}

func openValkey(config ValkeyConfig, prefix string) (KVStore, error) {
	opts, err := valkeyConnectionOptions(config)
	if err != nil {
		return nil, err
	}

	// Create client
	client, err := valkey.NewClient(opts)
	if err != nil {
		return nil, valkeyConnectionError(err)
	}

	store := &ValkeyStore{
		client:           client,
		prefix:           prefix,
		config:           config,
		operationTimeout: opts.ConnWriteTimeout,
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		client.Close()
		return nil, valkeyConnectionError(err)
	}

	// Initialize pub/sub
	store.pubsub = newValkeyPubSub(client, prefix)
	store.pubsub.operationTimeout = opts.ConnWriteTimeout

	log.Info().
		Int("db", opts.SelectDB).
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
	cmd := v.client.B().Get().Key(v.prefix + key).Build()
	resp := v.do(ctx, cmd)

	val, err := resp.AsBytes()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get key %s: %w", key, err)
	}

	return val, nil
}

// GetBounded atomically checks length before returning a payload from Valkey.
func (v *ValkeyStore) GetBounded(ctx context.Context, key string, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, ErrInvalidReadLimit
	}
	const script = `if redis.call('EXISTS', KEYS[1]) == 0 then return false end
if redis.call('STRLEN', KEYS[1]) > tonumber(ARGV[1]) then return redis.error_reply('STARPORT_VALUE_TOO_LARGE') end
return redis.call('GET', KEYS[1])`
	cmd := v.client.B().Eval().Script(script).Numkeys(1).Key(v.prefix + key).Arg(strconv.Itoa(maxBytes)).Build()
	value, err := v.do(ctx, cmd).AsBytes()
	if valkey.IsValkeyNil(err) {
		return nil, ErrNotFound
	}
	if err != nil && err.Error() == "STARPORT_VALUE_TOO_LARGE" {
		return nil, ErrValueTooLarge
	}
	if err != nil {
		return nil, fmt.Errorf("bounded storage read: %w", err)
	}
	return value, nil
}

// Set stores a value with a key
func (v *ValkeyStore) Set(ctx context.Context, key string, value []byte) error {
	cmd := v.client.B().Set().Key(v.prefix + key).Value(string(value)).Build()
	resp := v.do(ctx, cmd)

	if err := resp.Error(); err != nil {
		return fmt.Errorf("failed to set key %s: %w", key, err)
	}

	return nil
}

// Delete removes a key
func (v *ValkeyStore) Delete(ctx context.Context, key string) error {
	cmd := v.client.B().Del().Key(v.prefix + key).Build()
	resp := v.do(ctx, cmd)

	if err := resp.Error(); err != nil {
		return fmt.Errorf("failed to delete key %s: %w", key, err)
	}

	return nil
}

// Exists checks if a key exists
func (v *ValkeyStore) Exists(ctx context.Context, key string) (bool, error) {
	cmd := v.client.B().Exists().Key(v.prefix + key).Build()
	resp := v.do(ctx, cmd)

	count, err := resp.AsInt64()
	if err != nil {
		return false, fmt.Errorf("failed to check key existence %s: %w", key, err)
	}

	return count > 0, nil
}

// TTL operations

// SetWithTTL stores a value with expiration
func (v *ValkeyStore) SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	cmd := v.client.B().Set().Key(v.prefix + key).Value(string(value)).Ex(ttl).Build()
	resp := v.do(ctx, cmd)

	if err := resp.Error(); err != nil {
		return fmt.Errorf("failed to set key with TTL %s: %w", key, err)
	}

	return nil
}

// GetTTL returns the remaining TTL for a key
func (v *ValkeyStore) GetTTL(ctx context.Context, key string) (time.Duration, error) {
	cmd := v.client.B().Ttl().Key(v.prefix + key).Build()
	resp := v.do(ctx, cmd)

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
	cmd := v.client.B().Expireat().Key(v.prefix + key).Timestamp(expireAt.Unix()).Build()
	resp := v.do(ctx, cmd)

	if err := resp.Error(); err != nil {
		return fmt.Errorf("failed to set expiration for key %s: %w", key, err)
	}

	return nil
}

// Atomic operations

// Increment atomically increments a value
func (v *ValkeyStore) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	cmd := v.client.B().Incrby().Key(v.prefix + key).Increment(delta).Build()
	resp := v.do(ctx, cmd)

	val, err := resp.AsInt64()
	if err != nil {
		return 0, fmt.Errorf("failed to increment key %s: %w", key, err)
	}

	return val, nil
}

// Decrement atomically decrements a value
func (v *ValkeyStore) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	cmd := v.client.B().Decrby().Key(v.prefix + key).Decrement(delta).Build()
	resp := v.do(ctx, cmd)

	val, err := resp.AsInt64()
	if err != nil {
		return 0, fmt.Errorf("failed to decrement key %s: %w", key, err)
	}

	return val, nil
}

// CompareAndSwap atomically updates a value if it matches the old value
func (v *ValkeyStore) CompareAndSwap(ctx context.Context, key string, old, newValue []byte) error {
	return v.CompareAndSwapBatch(ctx, []CompareAndSwapMutation{{
		Key: key, ExpectedValue: old, NewValue: newValue,
	}})
}

// CompareAndSwapBatch applies all conditional writes or none of them.
func (v *ValkeyStore) CompareAndSwapBatch(ctx context.Context, mutations []CompareAndSwapMutation) error {
	if err := validateCompareAndSwapMutations(mutations); err != nil {
		return err
	}
	if len(mutations) == 0 {
		return nil
	}
	script := `
		for index = 1, #KEYS do
			local offset = (index - 1) * 5
			local current = redis.call("get", KEYS[index])
			if ARGV[offset + 1] == "0" then
				if current ~= false then return 0 end
			elseif current == false or current ~= ARGV[offset + 2] then
				return 0
			end
		end
		for index = 1, #KEYS do
			local offset = (index - 1) * 5
			if ARGV[offset + 3] == "0" then
				redis.call("del", KEYS[index])
			elseif tonumber(ARGV[offset + 5]) > 0 then
				redis.call("set", KEYS[index], ARGV[offset + 4], "PX", ARGV[offset + 5])
			else
				redis.call("set", KEYS[index], ARGV[offset + 4], "KEEPTTL")
			end
		end
		return 1
	`

	keys := make([]string, 0, len(mutations))
	args := make([]string, 0, len(mutations)*5)
	for _, mutation := range mutations {
		expectedExists := "1"
		if mutation.ExpectedValue == nil {
			expectedExists = "0"
		}
		newExists := "1"
		if mutation.NewValue == nil {
			newExists = "0"
		}
		ttlMilliseconds := mutation.TTL.Milliseconds()
		if mutation.TTL > 0 && ttlMilliseconds == 0 {
			ttlMilliseconds = 1
		}
		keys = append(keys, v.prefix+mutation.Key)
		args = append(args, expectedExists, string(mutation.ExpectedValue), newExists, string(mutation.NewValue), fmt.Sprint(ttlMilliseconds))
	}
	cmd := v.client.B().Eval().Script(script).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build()
	resp := v.do(ctx, cmd)

	// Check for errors first
	if err := resp.Error(); err != nil {
		return fmt.Errorf("failed to compare and swap %d keys: %w", len(keys), err)
	}

	updated, err := resp.AsInt64()
	if err != nil {
		return fmt.Errorf("decode compare-and-swap batch result: %w", err)
	}
	if updated != 1 {
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
	cmd := v.client.B().Mget().Key(v.physicalKeys(keys)...).Build()
	resp := v.do(ctx, cmd)

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
		cmd := v.client.B().Set().Key(v.prefix + key).Value(string(value)).Build()
		results = append(results, v.do(ctx, cmd))
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

	cmd := v.client.B().Del().Key(v.physicalKeys(keys)...).Build()
	resp := v.do(ctx, cmd)

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
		cmd := v.client.B().Set().Key(v.prefix + key).Value(string(value)).Ex(ttl).Build()
		results = append(results, v.do(ctx, cmd))
	}

	// Check all results
	for i, result := range results {
		if err := result.Error(); err != nil {
			return fmt.Errorf("failed to set key with TTL in batch at index %d: %w", i, err)
		}
	}

	return nil
}

// Scan operations

// Scan returns keys matching a pattern
func (v *ValkeyStore) Scan(ctx context.Context, pattern string, limit int) ([]string, error) {
	var keys []string
	cursor := uint64(0)
	// COUNT controls scan work, not the number of matching results. A small
	// result limit must not force one network request per unrelated key.
	const count int64 = 1000

	for {
		cmd := v.client.B().Scan().Cursor(cursor).Match(v.prefix + pattern).Count(count).Build()
		resp := v.do(ctx, cmd)

		// Parse scan result
		scanResult, err := resp.AsScanEntry()
		if err != nil {
			return nil, fmt.Errorf("failed to scan keys with pattern %s: %w", pattern, err)
		}

		for _, physical := range scanResult.Elements {
			if logical, ok := strings.CutPrefix(physical, v.prefix); ok {
				keys = append(keys, logical)
			}
		}

		// Check if we've reached the limit or finished scanning
		if scanResult.Cursor == 0 || (limit > 0 && len(keys) >= limit) {
			break
		}

		cursor = scanResult.Cursor
	}

	if limit > 0 && len(keys) > limit {
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
	resp := v.do(ctx, cmd)

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

// Ensure ValkeyStore implements KVStore interface
var _ KVStore = (*ValkeyStore)(nil)

// Ensure ValkeyStore implements PubSubProvider interface
var _ PubSubProvider = (*ValkeyStore)(nil)

func (v *ValkeyStore) physicalKeys(logical []string) []string {
	keys := make([]string, len(logical))
	for i, key := range logical {
		keys[i] = v.prefix + key
	}
	return keys
}
