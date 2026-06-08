package governance

import "context"

type runPauseHolderKey struct{}

type runPauseHolder struct {
	Pause *RunPause
}

// RunPause is captured when a high-risk tool is blocked pending approval.
type RunPause struct {
	ApprovalID string
	ToolName   string
	Arguments  string
}

func ContextWithPauseHolder(ctx context.Context) (context.Context, *runPauseHolder) {
	h := &runPauseHolder{}
	return context.WithValue(ctx, runPauseHolderKey{}, h), h
}

func setRunPause(ctx context.Context, p *RunPause) {
	if h, ok := ctx.Value(runPauseHolderKey{}).(*runPauseHolder); ok && h != nil {
		h.Pause = p
	}
}

// RunPauseFromContext returns the pause set during the last tool middleware invocation.
func RunPauseFromContext(ctx context.Context) *RunPause {
	h, _ := ctx.Value(runPauseHolderKey{}).(*runPauseHolder)
	if h == nil {
		return nil
	}
	return h.Pause
}
