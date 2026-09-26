package app

import "github.com/agentstation/starport/internal/authorization"

// admissionReady checks common admission prerequisites from retained memory.
// Per-caller policy, credentials, and budgets still govern each request.
func (a *App) admissionReady() bool {
	if a.authorization == nil || !a.authorization.cache.Ready() || a.registry == nil {
		return false
	}
	return a.registry.AdmissionReady()
}

// authorizationStatus reports local gateway policy separately from catalog permission.
func (a *App) authorizationStatus() authorization.Status {
	if a.authorization == nil {
		return (*authorization.Cache)(nil).Status()
	}
	status := a.authorization.cache.Status()
	status.Observations = a.authorization.monitor.Status()
	return status
}
