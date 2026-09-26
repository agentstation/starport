package controllers

// CacheFillStatus describes optional cache work for the operator API.
type CacheFillStatus struct {
	Shared          SharedCacheStatus `json:"shared"`
	Enabled         bool              `json:"enabled"`
	EntryLimit      int64             `json:"entry_limit"`
	ByteLimit       int64             `json:"byte_limit"`
	WorkerLimit     int64             `json:"worker_limit"`
	RetainedEntries int64             `json:"retained_entries"`
	RetainedBytes   int64             `json:"retained_bytes"`
	ActiveFills     int64             `json:"active_fills"`
	DroppedFills    uint64            `json:"dropped_fills"`
	FailedFills     uint64            `json:"failed_fills"`
	CompletedFills  uint64            `json:"completed_fills"`
}

// SharedCacheStatus describes the optional cache connection.
type SharedCacheStatus struct {
	Configured bool   `json:"configured"`
	Available  bool   `json:"available"`
	State      string `json:"state"`
	KeyPrefix  string `json:"key_prefix"`
}

func (h *AdminController) responseCacheStatus() CacheFillStatus {
	if h.deployment.ResponseCache == nil {
		return CacheFillStatus{}
	}
	return h.deployment.ResponseCache()
}

func (h *AdminController) extractionCacheStatus() CacheFillStatus {
	if h.deployment.ExtractionCache == nil {
		return CacheFillStatus{}
	}
	return h.deployment.ExtractionCache()
}
