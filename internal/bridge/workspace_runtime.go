package bridge

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"strings"

	"agentgo/internal/governance"
	"agentgo/internal/skills"
	"agentgo/internal/tools"
	"agentgo/internal/workspace"
)

func (r *Runtime) WorkspaceInfo() WorkspaceInfo {
	if r == nil {
		return WorkspaceInfo{}
	}
	return buildWorkspaceInfo(r.WorkspaceRoot())
}

// ErrWorkspaceBusy is returned when a switch is refused because work bound to
// the current workspace is still in flight.
var ErrWorkspaceBusy = errors.New("workspace is busy")

// workspaceBinder rebinds one workspace-scoped resource (for example the code
// tools and their language servers) to root. A returned error aborts the
// switch and rolls the runtime back to the previous workspace.
type workspaceBinder func(root string) error

type workspaceBinderEntry struct {
	id int
	fn workspaceBinder
}

// AddWorkspaceBinder registers fn to run after every effective workspace
// change, in registration order. The returned func unregisters it.
func (r *Runtime) AddWorkspaceBinder(fn workspaceBinder) (remove func()) {
	r.bindMu.Lock()
	defer r.bindMu.Unlock()
	r.nextBinderID++
	id := r.nextBinderID
	r.wsBinders = append(r.wsBinders, workspaceBinderEntry{id: id, fn: fn})
	return func() {
		r.bindMu.Lock()
		defer r.bindMu.Unlock()
		for i, e := range r.wsBinders {
			if e.id == id {
				r.wsBinders = append(r.wsBinders[:i:i], r.wsBinders[i+1:]...)
				return
			}
		}
	}
}

func (r *Runtime) runWorkspaceBinders(root string) error {
	r.bindMu.Lock()
	entries := append([]workspaceBinderEntry(nil), r.wsBinders...)
	r.bindMu.Unlock()
	for _, e := range entries {
		if err := e.fn(root); err != nil {
			return err
		}
	}
	return nil
}

// workspaceBusyReason is empty when the workspace may be switched. Tools bind
// to the workspace of the run that started them, so a switch is refused while a
// run is in flight or waiting for an approval it will resume with.
func (r *Runtime) workspaceBusyReason() string {
	if n := r.agentRunner.ActiveRuns(); n > 0 {
		return fmt.Sprintf("%d run(s) in progress; stop them first", n)
	}
	if n := r.pending.Len(); n > 0 {
		return fmt.Sprintf("%d request(s) waiting for approval; resolve them first", n)
	}
	return ""
}

func sameWorkspacePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	return runtime.GOOS == "windows" && strings.EqualFold(a, b)
}

// SetWorkspaceRoot switches the workspace as one unit: refuse while busy,
// update every workspace-scoped component, then run the registered binders.
// If a binder fails the previous workspace is restored, so a failed switch
// leaves the old one fully usable instead of half-moved.
func (r *Runtime) SetWorkspaceRoot(path string) (WorkspaceInfo, error) {
	abs, err := normalizeWorkspaceRoot(path)
	if err != nil {
		return WorkspaceInfo{}, err
	}
	prev := r.WorkspaceRoot()
	changed := !sameWorkspacePath(prev, abs)
	if changed {
		if why := r.workspaceBusyReason(); why != "" {
			return WorkspaceInfo{}, fmt.Errorf("%w: %s", ErrWorkspaceBusy, why)
		}
	}
	if err := r.applyWorkspaceRoot(abs); err != nil {
		return WorkspaceInfo{}, err
	}
	if !changed {
		return buildWorkspaceInfo(abs), nil
	}
	if err := r.runWorkspaceBinders(abs); err != nil {
		if prev == "" {
			return WorkspaceInfo{}, fmt.Errorf("switch workspace to %s: %w", abs, err)
		}
		if rerr := r.applyWorkspaceRoot(prev); rerr != nil {
			return WorkspaceInfo{}, errors.Join(fmt.Errorf("switch workspace to %s: %w", abs, err), fmt.Errorf("restore %s: %w", prev, rerr))
		}
		if berr := r.runWorkspaceBinders(prev); berr != nil {
			log.Printf("[workspace] rebind after rollback to %s: %v", prev, berr)
		}
		return WorkspaceInfo{}, fmt.Errorf("switch workspace to %s failed, kept %s: %w", abs, prev, err)
	}
	return buildWorkspaceInfo(abs), nil
}

// applyWorkspaceRoot updates the runtime-owned, workspace-scoped state.
func (r *Runtime) applyWorkspaceRoot(abs string) error {
	var err error
	wsMW := workspace.NewContextMiddleware(abs)
	loader := skills.NewLoader(abs)
	loader.Reload()

	r.mu.Lock()
	r.workspace = abs
	r.wsMiddleware = wsMW
	r.skillLoader = loader
	policy := governance.BuildPolicy(r.governanceCfg.ControlMode, abs)
	if r.toolReg != nil {
		if err := tools.RegisterWorkspaceBoundTools(r.toolReg, abs); err != nil {
			r.mu.Unlock()
			return err
		}
		_ = tools.RegisterActivateSkill(r.toolReg, loader)
	}
	if r.agentRunner != nil {
		r.agentRunner.SetWorkspaceRoot(abs, wsMW, policy)
	}
	err = saveAppConfig(r.dataDir, r.llm, r.governanceCfg, WorkspaceConfig{Root: abs})
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if r.workspaceReviews != nil {
		r.workspaceReviews.clear()
	}
	return nil
}
