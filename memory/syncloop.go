package memory

import (
	"context"
	"log/slog"
	"time"
)

// SyncLoop runs periodic sync in the background (invariant 13: non-blocking).
// Pulls remote changes, then pushes local changes.
// Stops when ctx is cancelled. Does a final push attempt on shutdown.
func SyncLoop(ctx context.Context, syncer *Syncer, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := syncer.Fetch(); err != nil {
				slog.Warn("memory.SyncLoop: pull failed", "err", err)
			}
			if err := syncer.CommitAndPush(ctx); err != nil {
				slog.Warn("memory.SyncLoop: push failed", "err", err)
			}
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := syncer.CommitAndPush(shutdownCtx); err != nil {
				slog.Warn("memory.SyncLoop: final push failed", "err", err)
			}
			cancel()
			return
		}
	}
}
