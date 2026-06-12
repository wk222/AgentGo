package governance

import (
	"context"
	"log"
	"time"
)

// StartApprovalEscalation periodically expires stale pending tool approvals.
func StartApprovalEscalation(ctx context.Context, queue *ApprovalQueue, interval, maxAge time.Duration) {
	if queue == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	if maxAge <= 0 {
		maxAge = 30 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := queue.ExpirePendingOlderThan(ctx, maxAge)
				if err != nil {
					log.Printf("[governance] approval expire: %v", err)
					continue
				}
				if n > 0 {
					log.Printf("[governance] expired %d stale pending approval(s)", n)
				}
			}
		}
	}()
}
