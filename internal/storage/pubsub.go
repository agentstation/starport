package storage

import "context"

// PubSubClient defines the interface for pub/sub operations used for cache invalidation
type PubSubClient interface {
	// Subscribe to a pattern and handle messages
	Subscribe(pattern string, handler func(channel, message string)) error
	// Publish a message to a channel
	Publish(ctx context.Context, channel string, message string) error
	// Close the pub/sub client
	Close() error
}

// PubSubProvider is implemented by storage backends that support pub/sub
type PubSubProvider interface {
	GetPubSub() PubSubClient
}
