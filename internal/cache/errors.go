package cache

import "errors"

// Common cache errors
var (
	// ErrPubSubClosed is returned when operations are attempted on a closed PubSub client
	ErrPubSubClosed = errors.New("pubsub client is closed")
	// ErrCacheClosed is returned when operations are attempted on a closed cache
	ErrCacheClosed = errors.New("cache is closed")
	// ErrInvalidKey is returned when an invalid key is provided
	ErrInvalidKey = errors.New("invalid cache key")
	// ErrInvalidTTL is returned when an invalid TTL is provided
	ErrInvalidTTL = errors.New("invalid TTL value")
)
