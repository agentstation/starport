package authorization

// Ready checks shared admission prerequisites without loading caller policy.
// It neither reads storage nor creates or renews permission receipts.
func (c *Cache) Ready() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return false
	}
	now, healthy := c.clock()
	if !healthy || now.IsZero() {
		return false
	}
	if elapsed, known := c.elapsed(); !known || elapsed < 0 {
		return false
	}
	for _, fence := range c.authorities.fences {
		if _, err := fence.Start(); err != nil {
			return false
		}
	}
	return true
}
