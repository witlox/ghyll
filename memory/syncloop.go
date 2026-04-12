package memory

import (
	"context"
	"log"
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
				log.Printf("sync pull: %v", err)
			}
			if err := syncer.CommitAndPush(ctx); err != nil {
				log.Printf("sync push: %v", err)
			}
		case <-ctx.Done():
			// Final push attempt on shutdown
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := syncer.CommitAndPush(shutdownCtx); err != nil {
				log.Printf("final sync push: %v", err)
			}
			cancel()
			return
		}
	}
}
