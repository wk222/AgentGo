package sessions

import "context"

// SpineSnapshot carries cross-cutting runtime lines injected via context (PyBot ProjectedRuntimeView).
type SpineSnapshot struct {
	WorkflowContext []string
	WorkspaceRecent []string
	EpisodicSummary []string
	ContextHygiene  []string
	TeamMemory      []string
	Isolation       []string
}

type spineSnapshotKey struct{}

func WithSpineSnapshot(ctx context.Context, snap SpineSnapshot) context.Context {
	return context.WithValue(ctx, spineSnapshotKey{}, snap)
}

func SpineSnapshotFromContext(ctx context.Context) SpineSnapshot {
	v, ok := ctx.Value(spineSnapshotKey{}).(SpineSnapshot)
	if !ok {
		return SpineSnapshot{}
	}
	return v
}

// AppendWorkflowLine adds a matrix/workflow status line to the context snapshot.
func AppendWorkflowLine(ctx context.Context, line string) context.Context {
	if line == "" {
		return ctx
	}
	snap := SpineSnapshotFromContext(ctx)
	snap.WorkflowContext = append(snap.WorkflowContext, line)
	return WithSpineSnapshot(ctx, snap)
}

// AppendWorkspaceRecent records a touched file path on the spine snapshot.
func AppendWorkspaceRecent(ctx context.Context, relPath string) context.Context {
	if relPath == "" {
		return ctx
	}
	snap := SpineSnapshotFromContext(ctx)
	line := "recent: " + relPath
	snap.WorkspaceRecent = append(snap.WorkspaceRecent, line)
	return WithSpineSnapshot(ctx, snap)
}

// AppendTeamMemory adds a team-scope memory line.
func AppendTeamMemory(ctx context.Context, line string) context.Context {
	if line == "" {
		return ctx
	}
	snap := SpineSnapshotFromContext(ctx)
	snap.TeamMemory = append(snap.TeamMemory, line)
	return WithSpineSnapshot(ctx, snap)
}
