package catalog

import (
	"context"
	"sync"
	"time"
)

// renewFleetAdoption keeps validation within the native maintenance grant.
// A failed renewal cancels validation before the selection can change.
func renewFleetAdoption(ctx context.Context, maintenance *fleetMaintenance, cancel context.CancelFunc) func() error {
	stop, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var failure error
	go func() {
		defer close(done)
		timer := time.NewTicker(fleetRetentionLockLifetime / 3)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-timer.C:
				if err := maintenance.mutate(ctx, nil); err != nil {
					failure = err
					cancel()
					return
				}
			}
		}
	}()
	return func() error { once.Do(func() { close(stop) }); <-done; return failure }
}
