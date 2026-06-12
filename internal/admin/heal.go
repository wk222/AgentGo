package admin

import (
	"context"
	"log"

	"agentgo/internal/capability"
)

// SubscribeRuntimeHeal listens for app.runtime_error events and enqueues admin fix tasks.
func (r *AdminRunner) SubscribeRuntimeHeal(bus *capability.Bus) {
	if r == nil || bus == nil {
		return
	}
	bus.Subscribe(func(ev capability.Event) {
		if ev.Type != capability.EventAppRuntimeError {
			return
		}
		appName := ev.Source
		errMsg := ev.Payload["error"]
		ctx := context.Background()
		if r.onHeal != nil {
			r.onHeal(ctx, appName, errMsg)
			return
		}
		task, err := r.EnqueueRuntimeHealTask(ctx, appName, errMsg)
		if err != nil {
			log.Printf("[AdminRunner] runtime heal enqueue failed: %v", err)
			return
		}
		log.Printf("[AdminRunner] enqueued runtime heal task %s for app %s", task.ID, appName)
	})
}
