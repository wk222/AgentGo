package governance

import (
	"context"
	"log"
	"time"
)

// StartApprovalEscalation periodically expires stale pending tool approvals.
func StartApprovalEscalation(ctx context.Context, queue *ApprovalQueue, interval, maxAge time.Duration) {
	StartApprovalEscalationNotify(ctx, queue, interval, maxAge, nil)
}

// StartApprovalEscalationNotify is StartApprovalEscalation that also calls
// afterExpire(n) after every successful pass, with the number of approvals this
// pass expired. It runs even when n is 0: processes sharing a database expire
// each other's approvals, so a process cannot rely on its own count to know
// that runs it holds have lost their approval.
func StartApprovalEscalationNotify(ctx context.Context, queue *ApprovalQueue, interval, maxAge time.Duration, afterExpire func(n int64)) {
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
				if afterExpire != nil {
					afterExpire(n)
				}
			}
		}
	}()
}
