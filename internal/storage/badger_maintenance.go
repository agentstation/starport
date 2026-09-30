package storage

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// StartMaintenance starts native Badger maintenance after startup admission.
// Repeated calls retain the same workers. Closed or imported stores refuse startup.
func (s *BadgerStore) StartMaintenance() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := InspectRecoveryStartup(ctx, s); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly {
		return ErrReadOnly
	}
	if s.closed {
		return ErrStorageClosed
	}
	if s.config.InMemory || s.gcTicker != nil {
		return nil
	}
	if !s.config.SyncWrites {
		log.Warn().Msg("Badger sync_writes=false permits acknowledged-write loss after a host failure")
	}
	s.startGarbageCollection(s.collectValueLog)
	s.startCompaction()
	return nil
}
