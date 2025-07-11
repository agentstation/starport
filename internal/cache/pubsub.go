package cache

import (
	"context"
	"strings"
	"sync"

	"github.com/agentstation/starport/internal/storage"
	"github.com/rs/zerolog/log"
)

// PubSubClient re-exports storage.PubSubClient for convenience
type PubSubClient = storage.PubSubClient

// PubSubProvider re-exports storage.PubSubProvider for convenience
type PubSubProvider = storage.PubSubProvider

// NoopPubSub is a no-op implementation for single-node deployments
type NoopPubSub struct{}

// Subscribe does nothing in noop implementation
func (n *NoopPubSub) Subscribe(pattern string, _ func(channel, message string)) error {
	log.Debug().Str("pattern", pattern).Msg("noop pubsub subscribe")
	return nil
}

// Publish does nothing in noop implementation
func (n *NoopPubSub) Publish(_ context.Context, channel string, _ string) error {
	log.Debug().Str("channel", channel).Msg("noop pubsub publish")
	return nil
}

// Close does nothing in noop implementation
func (n *NoopPubSub) Close() error {
	return nil
}

// MemoryPubSub is an in-memory pub/sub implementation for testing
type MemoryPubSub struct {
	mu          sync.RWMutex
	subscribers map[string][]func(channel, message string)
	closed      bool
}

// NewMemoryPubSub creates a new in-memory pub/sub client
func NewMemoryPubSub() *MemoryPubSub {
	return &MemoryPubSub{
		subscribers: make(map[string][]func(channel, message string)),
	}
}

// Subscribe adds a handler for a pattern
func (m *MemoryPubSub) Subscribe(pattern string, handler func(channel, message string)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrPubSubClosed
	}

	m.subscribers[pattern] = append(m.subscribers[pattern], handler)
	log.Debug().Str("pattern", pattern).Msg("subscribed to pattern")
	return nil
}

// Publish sends a message to all matching subscribers
func (m *MemoryPubSub) Publish(_ context.Context, channel string, message string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return ErrPubSubClosed
	}

	// Simple pattern matching
	for pattern, handlers := range m.subscribers {
		// Check if channel matches pattern
		if matchesPattern(pattern, channel) {
			for _, handler := range handlers {
				// Run handlers in goroutines to avoid blocking
				go handler(channel, message)
			}
		}
	}

	log.Debug().
		Str("channel", channel).
		Str("message", message).
		Msg("published message")

	return nil
}

// Close closes the pub/sub client
func (m *MemoryPubSub) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	m.subscribers = nil
	return nil
}

// Invalidation channel constants
const (
	// Channel prefixes for different data types
	ChannelAPIKeyInvalidate = "cache:inv:apikey:" // #nosec G101 -- This is a channel name, not a credential
	ChannelPresetInvalidate = "cache:inv:preset:"
	ChannelModelInvalidate  = "cache:inv:model:"

	// Broadcast channels for bulk invalidation
	ChannelAPIKeyFlush = "cache:flush:apikey" // #nosec G101 -- This is a channel name, not a credential
	ChannelPresetFlush = "cache:flush:preset"
	ChannelModelFlush  = "cache:flush:model"
)

// matchesPattern checks if a channel matches a pattern with * wildcard
func matchesPattern(pattern, channel string) bool {
	// Exact match
	if pattern == channel {
		return true
	}

	// Pattern with wildcard
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(channel, prefix)
	}

	return false
}
