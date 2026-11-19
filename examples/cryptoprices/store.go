package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type snapshot struct {
	Coins     []viewCoin
	UpdatedAt time.Time
	Err       string
}

// store keeps the most recent price snapshot and refreshes it from the
// upstream API on a fixed interval.
type store struct {
	src      *priceSource
	ids      []string
	ttl      time.Duration
	dataPath string
	log      *slog.Logger

	mu          sync.RWMutex
	refreshMu   sync.Mutex
	snap        snapshot
	lastAttempt time.Time
}

// minRetryGap keeps a rate-limited upstream from being retried by every
// browser poll.
const minRetryGap = 15 * time.Second

func newStore(cfg config, src *priceSource, log *slog.Logger) *store {
	return &store{
		src:      src,
		ids:      cfg.coinIDs,
		ttl:      cfg.refresh,
		dataPath: cfg.dataFile,
		log:      log,
	}
}

// Refresh fetches a new snapshot. On failure the previous prices are kept and
// the error is reported to clients alongside them.
func (s *store) Refresh(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	s.mu.Lock()
	s.lastAttempt = time.Now()
	s.mu.Unlock()

	coins, err := s.src.Fetch(ctx, s.ids)

	if err != nil {
		s.mu.Lock()
		s.snap.Err = err.Error()
		s.mu.Unlock()
		return err
	}

	now := time.Now()
	s.mu.Lock()
	s.snap = snapshot{
		Coins:     buildViews(coins),
		UpdatedAt: now,
	}
	s.mu.Unlock()

	// The data file is a convenience for other code; a write failure must not
	// take the dashboard down.
	if err := writePriceFile(s.dataPath, coins, now); err != nil {
		s.log.Warn("could not write price data file", "path", s.dataPath, "error", err)
	}
	return nil
}

func (s *store) Snapshot() snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// RefreshIfStale refreshes when prices are missing or older than the TTL,
// retrying failed attempts at most once per minRetryGap.
func (s *store) RefreshIfStale(ctx context.Context) {
	s.mu.RLock()
	snap := s.snap
	lastAttempt := s.lastAttempt
	s.mu.RUnlock()

	if len(snap.Coins) > 0 && time.Since(snap.UpdatedAt) <= s.ttl {
		return
	}
	if time.Since(lastAttempt) < minRetryGap {
		return
	}

	_ = s.Refresh(ctx)
}

// Run refreshes the snapshot in the background until ctx is cancelled.
func (s *store) Run(ctx context.Context) {
	ticker := time.NewTicker(s.ttl)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			if err := s.Refresh(refreshCtx); err != nil {
				s.log.Warn("price refresh failed", "error", err)
			}
			cancel()
		}
	}
}
