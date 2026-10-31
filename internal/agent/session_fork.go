package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// The plan task list is a handful of small JSON records; a directory that
	// looks like anything else is left alone rather than copied wholesale.
	forkMaxTaskFiles = 256
	forkMaxTaskBytes = 1 << 20
)

// AdoptedFork reports what AdoptForkedSession carried over.
type AdoptedFork struct {
	// Tasks is the number of plan task records copied for the new session.
	Tasks int
}

// AdoptForkedSession finishes a fork that sessions.Store.ForkSession has already
// committed. The database part is copied there; what is left is what the runner
// keeps for itself:
//   - the per-session mode, which the runner holds in memory and loaded at start,
//   - the plan task list, which lives in files under the workspace, not in the
//     database.
//
// A failure here leaves a usable fork behind (the conversation is complete), so
// callers report it as a warning rather than undoing the fork.
func (r *Runner) AdoptForkedSession(ctx context.Context, sourceID, forkID string) (AdoptedFork, error) {
	var out AdoptedFork
	if r == nil {
		return out, nil
	}
	sourceID = strings.TrimPrefix(strings.TrimSpace(sourceID), "agentic:")
	forkID = strings.TrimPrefix(strings.TrimSpace(forkID), "agentic:")
	if sourceID == "" || forkID == "" || sourceID == forkID {
		return out, errors.New("a fork needs two different session ids")
	}
	// The copied row is already in the database; only the in-memory map is stale.
	r.loadPersistedSessionModes()

	src := planTaskDirectory(WithSessionID(ctx, sourceID), r.workspaceRoot, r.dataDir)
	dst := planTaskDirectory(WithSessionID(ctx, forkID), r.workspaceRoot, r.dataDir)
	n, err := copyPlanTasks(src, dst)
	out.Tasks = n
	return out, err
}

// copyPlanTasks copies the plan task directory of one session to another. The
// copy is built beside the destination and renamed into place, so a failure
// never leaves a task list with some of its records missing. It returns the
// number of task records (N.json) copied; a missing source is not an error.
func copyPlanTasks(src, dst string) (int, error) {
	entries, err := os.ReadDir(src)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read plan tasks: %w", err)
	}
	if existing, err := os.ReadDir(dst); err == nil && len(existing) > 0 {
		return 0, fmt.Errorf("plan tasks of the new session already exist: %s", dst)
	}
	staging, err := os.MkdirTemp(filepath.Dir(dst), ".fork-*")
	if err != nil {
		return 0, fmt.Errorf("prepare plan task copy: %w", err)
	}
	defer os.RemoveAll(staging)

	files, tasks := 0, 0
	for _, e := range entries {
		// Records are plain files. Temporary files from an interrupted write,
		// links and directories are not part of the list.
		if !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".plantask-") {
			continue
		}
		if files++; files > forkMaxTaskFiles {
			return 0, fmt.Errorf("plan task directory has more than %d files", forkMaxTaskFiles)
		}
		if err := copyCapped(filepath.Join(src, e.Name()), filepath.Join(staging, e.Name()), forkMaxTaskBytes); err != nil {
			return 0, fmt.Errorf("copy plan task %s: %w", e.Name(), err)
		}
		if filepath.Ext(e.Name()) == ".json" {
			tasks++
		}
	}
	if files == 0 {
		return 0, nil
	}
	// An empty directory the new session created for itself may be in the way.
	_ = os.Remove(dst)
	if err := os.Rename(staging, dst); err != nil {
		return 0, fmt.Errorf("put plan tasks in place: %w", err)
	}
	return tasks, nil
}

// copyCapped copies one file, refusing to read more than limit bytes.
func copyCapped(src, dst string, limit int64) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(in, limit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("larger than %d bytes", limit)
	}
	return nil
}
