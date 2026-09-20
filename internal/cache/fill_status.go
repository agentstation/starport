package cache

// FillStatus reports optional response work without cache keys or payloads.
type FillStatus struct {
	Enabled         bool   `json:"enabled"`
	EntryLimit      int64  `json:"entry_limit"`
	ByteLimit       int64  `json:"byte_limit"`
	WorkerLimit     int64  `json:"worker_limit"`
	RetainedEntries int64  `json:"retained_entries"`
	RetainedBytes   int64  `json:"retained_bytes"`
	ActiveFills     int64  `json:"active_fills"`
	DroppedFills    uint64 `json:"dropped_fills"`
	FailedFills     uint64 `json:"failed_fills"`
	CompletedFills  uint64 `json:"completed_fills"`
}

// FillStatus reads counters and limits from memory.
func (cm *Manager) FillStatus() FillStatus {
	if cm == nil {
		return FillStatus{}
	}
	stats := cm.fills.stats()
	return FillStatus{Enabled: true, EntryLimit: fillQueueEntries, ByteLimit: fillQueueBytes, WorkerLimit: fillWorkers,
		RetainedEntries: stats.RetainedEntries, RetainedBytes: stats.RetainedBytes, ActiveFills: stats.ActiveFills,
		DroppedFills: stats.DroppedFills, FailedFills: stats.FailedFills, CompletedFills: stats.CompletedFills}
}
