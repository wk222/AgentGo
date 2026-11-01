package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// BackgroundBashStarter starts work as a background task and returns its task
// ID without waiting. The runtime that owns the background task manager (the
// agent package) provides it; this package cannot import that one.
type BackgroundBashStarter func(ctx context.Context, command string, work func(ctx context.Context) (string, error)) (taskID string, err error)

// SetBackgroundBash lets execute_bash honour run_in_background. Without a
// starter the argument is ignored and the command runs in the foreground, so
// nothing depends on the runtime having background support.
func (r *Registry) SetBackgroundBash(start BackgroundBashStarter) {
	if r == nil {
		return
	}
	r.bashBGMu.Lock()
	r.bashBG = start
	r.bashBGMu.Unlock()
}

func (r *Registry) backgroundBash() BackgroundBashStarter {
	if r == nil {
		return nil
	}
	r.bashBGMu.RLock()
	defer r.bashBGMu.RUnlock()
	return r.bashBG
}

// startBashInBackground runs the command as a background task and describes how
// to collect it. started is false when this registry has no background support,
// in which case the caller runs the command in the foreground as usual.
func startBashInBackground(ctx context.Context, r *Registry, root, command string, timeoutSeconds int) (out bashOutput, started bool, err error) {
	start := r.backgroundBash()
	if start == nil {
		return bashOutput{}, false, nil
	}
	id, err := start(ctx, command, func(workCtx context.Context) (string, error) {
		res, err := execBash(workCtx, root, command, timeoutSeconds)
		if err != nil {
			return "", err
		}
		raw, merr := json.Marshal(res)
		if merr != nil {
			return "", merr
		}
		return string(raw), nil
	})
	if err != nil {
		return bashOutput{}, true, err
	}
	return bashOutput{
		Message: fmt.Sprintf("Started in the background, task ID %s. The command is running now; call task_output with this ID "+
			"to read its output and exit code (it can wait for completion), or task_stop to end it. "+
			"Start other independent commands without waiting.", id),
	}, true, nil
}
