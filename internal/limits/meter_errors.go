package limits

import "errors"

var (
	// ErrCounterRequired reports a meter built without durable storage.
	ErrCounterRequired = errors.New("level counter is required")
	// ErrInvalidHolder reports an empty holder identity.
	ErrInvalidHolder = errors.New("level holder is required")
)
