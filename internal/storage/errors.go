package storage

import "errors"

// Transaction-related errors
var (
	// ErrTransactionClosed is returned when operations are attempted on a closed transaction
	ErrTransactionClosed = errors.New("transaction is closed")
	// ErrTransactionCommitted is returned when operations are attempted on a committed transaction
	ErrTransactionCommitted = errors.New("transaction already committed")
	// ErrTransactionRolledBack is returned when operations are attempted on a rolled back transaction
	ErrTransactionRolledBack = errors.New("transaction already rolled back")

	// PubSub-related errors
	// ErrPubSubClosed is returned when operations are attempted on a closed PubSub client
	ErrPubSubClosed = errors.New("pubsub client is closed")
)