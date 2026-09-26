package registry

// AdmissionReady checks the current catalog without acquiring provider resources.
// It cannot become the last lease whose release closes retired connectors.
func (r *Registry) AdmissionReady() bool {
	if r == nil {
		return false
	}
	generation := r.current.Load()
	if generation == nil {
		return false
	}
	generation.mu.Lock()
	unavailable := generation.closed || generation.draining
	generation.mu.Unlock()
	return !unavailable && generation.currentSnapshot().AllowsNewAttempt() && r.current.Load() == generation
}
