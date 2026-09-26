package cache

import "errors"

// ErrCacheClosed reports a fill submitted after shutdown.
var ErrCacheClosed = errors.New("cache is closed")
