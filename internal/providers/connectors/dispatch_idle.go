package connectors

import (
	"slices"
	"time"
)

func (t *dispatchTransport) maintainIdle() {
	t.mu.Lock()
	defer t.mu.Unlock()
	idleTimeout := t.base.IdleConnTimeout
	if idleTimeout <= 0 {
		idleTimeout = providerIdleConnectionTimeout
	}
	var idle []*dispatchConnection
	for _, entry := range t.connections {
		if entry.conn.Err() != nil || entry.conn.InFlight() != 0 {
			continue
		}
		remaining := idleTimeout - time.Since(time.Unix(0, entry.idleSince.Load()))
		if entry.retired.Load() || remaining <= 0 {
			_ = entry.conn.Close()
			continue
		}
		entry.timer.Reset(remaining)
		idle = append(idle, entry)
	}
	slices.SortFunc(idle, func(a, b *dispatchConnection) int {
		left, right := a.idleSince.Load(), b.idleSince.Load()
		if left > right {
			return -1
		}
		if left < right {
			return 1
		}
		return 0
	})
	perOrigin := t.base.MaxIdleConnsPerHost
	if perOrigin == 0 {
		perOrigin = 2
	}
	counts := make(map[string]int)
	for index, entry := range idle {
		counts[entry.origin]++
		if (t.base.MaxIdleConns > 0 && index >= t.base.MaxIdleConns) || (perOrigin > 0 && counts[entry.origin] > perOrigin) {
			_ = entry.conn.Close()
		}
	}
	t.connections = slices.DeleteFunc(t.connections, func(entry *dispatchConnection) bool {
		if entry.conn.Err() == nil {
			return false
		}
		entry.timer.Stop()
		return true
	})
	t.signal()
}
