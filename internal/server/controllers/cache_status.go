package controllers

import "github.com/agentstation/starport/internal/cache"

func (h *AdminController) responseCacheStatus() cache.FillStatus {
	if h.deployment.ResponseCache == nil {
		return cache.FillStatus{}
	}
	return h.deployment.ResponseCache()
}

func (h *AdminController) extractionCacheStatus() cache.FillStatus {
	if h.deployment.ExtractionCache == nil {
		return cache.FillStatus{}
	}
	return h.deployment.ExtractionCache()
}
