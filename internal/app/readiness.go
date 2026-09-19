package app

// admissionReady checks common admission prerequisites from retained memory.
// Per-caller policy, credentials, and budgets still govern each request.
func (a *App) admissionReady() bool {
	if a.authorization == nil || !a.authorization.cache.Ready() || a.registry == nil {
		return false
	}
	return a.registry.AdmissionReady()
}
