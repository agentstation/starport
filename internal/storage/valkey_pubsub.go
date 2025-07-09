package storage

import (
	"context"
	"fmt"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/valkey-io/valkey-go"
)

// ValkeyPubSub implements PubSubClient using Valkey pub/sub
type ValkeyPubSub struct {
	client        valkey.Client
	subscriptions map[string]context.CancelFunc
	handlers      map[string]func(channel, message string)
	mu            sync.RWMutex
	closed        bool
	wg            sync.WaitGroup
}

// NewValkeyPubSub creates a new Valkey pub/sub client
func NewValkeyPubSub(client valkey.Client) *ValkeyPubSub {
	return &ValkeyPubSub{
		client:        client,
		subscriptions: make(map[string]context.CancelFunc),
		handlers:      make(map[string]func(channel, message string)),
	}
}

// Subscribe subscribes to a pattern and handles messages
func (v *ValkeyPubSub) Subscribe(pattern string, handler func(channel, message string)) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.closed {
		return ErrPubSubClosed
	}

	// Check if already subscribed to this pattern
	if _, exists := v.subscriptions[pattern]; exists {
		return fmt.Errorf("already subscribed to pattern: %s", pattern)
	}

	// Create cancellable context for this subscription
	ctx, cancel := context.WithCancel(context.Background())
	v.subscriptions[pattern] = cancel
	v.handlers[pattern] = handler

	// Start goroutine to handle messages
	v.wg.Add(1)
	go func() {
		defer v.wg.Done()
		defer cancel()
		
		log.Info().Str("pattern", pattern).Msg("starting pubsub subscription")
		
		// Use client.Receive for pattern subscription
		err := v.client.Receive(ctx, v.client.B().Psubscribe().Pattern(pattern).Build(), func(msg valkey.PubSubMessage) {
			// For pattern messages, check if it has pattern field
			if msg.Pattern != "" {
				// Pattern message received
				handler(msg.Channel, msg.Message)
			}
			// Note: subscription confirmations are handled by OnSubscriptionHook if needed
		})
		
		if err != nil && err != context.Canceled {
			log.Error().Err(err).Str("pattern", pattern).Msg("pubsub receive error")
		}
		
		// Clean up
		v.mu.Lock()
		delete(v.subscriptions, pattern)
		delete(v.handlers, pattern)
		v.mu.Unlock()
	}()

	return nil
}

// Publish publishes a message to a channel
func (v *ValkeyPubSub) Publish(ctx context.Context, channel string, message string) error {
	v.mu.RLock()
	if v.closed {
		v.mu.RUnlock()
		return ErrPubSubClosed
	}
	v.mu.RUnlock()

	cmd := v.client.B().Publish().Channel(channel).Message(message).Build()
	resp := v.client.Do(ctx, cmd)
	
	if err := resp.Error(); err != nil {
		return fmt.Errorf("failed to publish to channel %s: %w", channel, err)
	}

	// Get number of receivers
	receivers, err := resp.AsInt64()
	if err != nil {
		return fmt.Errorf("failed to get publish response: %w", err)
	}

	log.Debug().
		Str("channel", channel).
		Int64("receivers", receivers).
		Msg("published message")

	return nil
}

// Close closes all subscriptions
func (v *ValkeyPubSub) Close() error {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return nil
	}
	v.closed = true
	
	// Cancel all subscriptions
	for pattern, cancel := range v.subscriptions {
		log.Debug().Str("pattern", pattern).Msg("cancelling subscription")
		cancel()
	}
	
	v.subscriptions = make(map[string]context.CancelFunc)
	v.handlers = make(map[string]func(channel, message string))
	v.mu.Unlock()

	// Wait for all subscription handlers to finish
	v.wg.Wait()
	
	log.Info().Msg("closed valkey pubsub client")
	return nil
}

// Ensure ValkeyPubSub implements PubSubClient interface
var _ PubSubClient = (*ValkeyPubSub)(nil)