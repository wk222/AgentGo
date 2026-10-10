//go:build windows

package trigger

import (
	"context"
	"os"
)

// watchProcess blocks on the process handle (no polling). A PID that cannot be opened is
// treated as already exited.
func watchProcess(ctx context.Context, pid int) (map[string]any, bool) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return map[string]any{"pid": pid, "note": "process not found, treated as exited"}, true
	}
	type res struct {
		code int
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		st, err := p.Wait()
		if err != nil {
			ch <- res{-1, err}
			return
		}
		ch <- res{st.ExitCode(), nil}
	}()
	select {
	case <-ctx.Done():
		return nil, false
	case r := <-ch:
		d := map[string]any{"pid": pid, "exit_code": r.code}
		if r.err != nil {
			d["note"] = r.err.Error()
		}
		return d, true
	}
}
