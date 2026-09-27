package storage

import "errors"

// PubSub errors
var (
	// PubSub-related errors
	// ErrPubSubClosed is returned when operations are attempted on a closed PubSub client
	ErrPubSubClosed = errors.New("pubsub client is closed")
)
