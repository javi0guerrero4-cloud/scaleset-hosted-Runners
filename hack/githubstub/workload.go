package main

import (
	"context"
	"time"
)

// runWorkload keeps demand moving so the live view shows the supervisor
// scaling: a burst of jobs arrives, some are canceled before a runner picks
// them up, and the rest run to completion.
func (s *stub) runWorkload(ctx context.Context) {
	const (
		burst    = 4
		canceled = 2
		cycle    = 26 * time.Second
	)

	if !sleep(ctx, 3*time.Second) {
		return
	}

	for {
		// The scale set may not exist yet while the supervisor starts up.
		if !s.enqueueDemand(burst) {
			if !sleep(ctx, 2*time.Second) {
				return
			}
			continue
		}

		if !sleep(ctx, 2500*time.Millisecond) {
			return
		}
		s.cancelDemand(canceled)

		if !sleep(ctx, cycle-2500*time.Millisecond) {
			return
		}
	}
}

func sleep(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
