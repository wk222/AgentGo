package bridge

import (
	"context"

	"agentgo/internal/core"
)

// Recovery of pending runs lives in core (see core/recovery.go). This file only
// wires it to this process's session store, approval queue and checkpoints.

func (rt *Runtime) recovery() *core.Recovery {
	r := &core.Recovery{Sessions: rt.Sessions(), Pending: rt.pending, ProcessAlive: processAlive}
	// Assign only non-nil values: a nil pointer inside an interface is not nil.
	if q := rt.Approvals(); q != nil {
		r.Approvals = q
	}
	if rt.cpStore != nil {
		r.Checkpoints = rt.cpStore
	}
	return r
}

// RecoverPending runs once at startup; see core.Recovery.Recover.
func (rt *Runtime) RecoverPending(ctx context.Context) (adopted, closed int) {
	return rt.recovery().Recover(ctx)
}

// sweepExpiredPending lets go of runs this process holds whose approval expired.
func (rt *Runtime) sweepExpiredPending(ctx context.Context) {
	rt.recovery().SweepExpired(ctx)
}
