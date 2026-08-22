package proxy

// Middleware wraps a Proxy with additional functionality. The caching,
// preset-resolution, and usage-capture seams each implement it, and the
// composition root layers them around the core proxy.
type Middleware interface {
	// Wrap wraps the given proxy with the middleware functionality
	Wrap(Proxy) Proxy
}
