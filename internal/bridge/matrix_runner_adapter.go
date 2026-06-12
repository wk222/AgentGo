package bridge

import (
	"context"

	"agentgo/internal/apps"
)

type matrixRunnerAdapter struct{ rt *Runtime }

func (a *matrixRunnerAdapter) InvokeApp(ctx context.Context, appID, capability, input string) (apps.InvokeResult, error) {
	return a.rt.InvokeInnerApp(ctx, appID, input, capability, "", ""), nil
}
