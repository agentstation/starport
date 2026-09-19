package authorization

import (
	"math"
	"sync/atomic"
	"time"
)

type elapsedValidity struct {
	clock    ElapsedClock
	start    time.Duration
	deadline time.Duration
	retired  atomic.Uint32
}

func (p *Permit) boundElapsed(clock ElapsedClock, start, lifetime time.Duration) error {
	if clock == nil || start < 0 || lifetime <= 0 || start > time.Duration(math.MaxInt64)-lifetime {
		return ErrUnavailable
	}
	p.elapsed = &elapsedValidity{clock: clock, start: start, deadline: start + lifetime}
	return p.elapsed.check()
}

func (v *elapsedValidity) check() error {
	switch v.retired.Load() {
	case 1:
		return ErrUnavailable
	case 2:
		return ErrExpired
	}
	now, known := v.clock()
	if !known || now < v.start {
		v.retired.CompareAndSwap(0, 1)
		return ErrUnavailable
	}
	if now >= v.deadline {
		v.retired.CompareAndSwap(0, 2)
		return ErrExpired
	}
	return nil
}
