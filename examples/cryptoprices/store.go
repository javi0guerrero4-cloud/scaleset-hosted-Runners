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
	src *priceSource
	ids []string
	ttl time.Duration
	log *slog.Logger

	mu        sync.RWMutex
	refreshMu sync.Mutex
	snap      snapshot
}

func newStore(cfg config, src *priceSource, log *slog.Logger) *store {
	return &store{
		src: src,
		ids: cfg.coinIDs,
		ttl: cfg.refresh,
		log: log,
	}
}

// Refresh fetches a new snapshot. On failure the previous prices are kept and
// the error is reported to clients alongside them.
func (s *store) Refresh(ctx context.Context) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	coins, err := s.src.Fetch(ctx, s.ids)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		s.snap.Err = err.Error()
		return err
	}

	s.snap = snapshot{
		Coins:     buildViews(coins),
		UpdatedAt: time.Now(),
	}
	return nil
}

func (s *store) Snapshot() snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
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
