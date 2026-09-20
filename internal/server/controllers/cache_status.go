package controllers

import "github.com/agentstation/starport/internal/cache"

func (h *AdminController) responseCacheStatus() cache.FillStatus {
	if h.deployment.ResponseCache == nil {
		return cache.FillStatus{}
	}
	return h.deployment.ResponseCache()
}
