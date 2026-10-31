package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/stretchr/testify/require"

	"agentgo/internal/governance"
)

// handlerTypeNames lists the concrete middleware types in mount order (outer -> inner).
func handlerTypeNames(hs []adk.ChatModelAgentMiddleware) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, strings.TrimPrefix(fmt.Sprintf("%T", h), "*"))
	}
	return out
}

func indexOfType(names []string, sub string) int {
	for i, n := range names {
		if strings.Contains(n, sub) {
			return i
		}
	}
	return -1
}

func countOfType(names []string, sub string) int {
	c := 0
	for _, n := range names {
		if strings.Contains(n, sub) {
			c++
		}
	}
	return c
}

func newPipelineTestRunner(t *testing.T) *Runner {
	t.Helper()
	q, err := governance.NewApprovalQueue(filepath.Join(t.TempDir(), "approvals.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })
	dir := t.TempDir()
	return &Runner{queue: q, dataDir: dir, workspaceRoot: dir}
}

// TestBuildADKHandlers_OrderAndDedup locks the main-agent middleware list:
// each guard exactly once, with a fixed relative order.
func TestBuildADKHandlers_OrderAndDedup(t *testing.T) {
	r := newPipelineTestRunner(t)
	hs, err := r.BuildADKHandlers(context.Background(), nil)
	require.NoError(t, err)
	names := handlerTypeNames(hs)
	t.Logf("handlers: %v", names)

	// Exactly one of each guard.
	require.Equal(t, 1, countOfType(names, "GovernanceMiddleware"), "governance must be mounted once: %v", names)
	require.Equal(t, 1, countOfType(names, "SubagentMonotoneMiddleware"), names)
	require.Equal(t, 1, countOfType(names, "SessionSpineMiddleware"), names)
	require.Equal(t, 1, countOfType(names, "ModeProfileMiddleware"), names)
	require.Equal(t, 1, countOfType(names, "ADKToolHealMiddleware"), "tool heal must be mounted once: %v", names)
	require.Equal(t, 1, countOfType(names, "patchtoolcalls"), names)
	require.Equal(t, 0, countOfType(names, "ToolResultPruner"), "pruner was merged into the spine: %v", names)

	// Fixed relative order: mode -> governance -> monotone -> spine -> safe-tool (last).
	mode := indexOfType(names, "ModeProfileMiddleware")
	gov := indexOfType(names, "GovernanceMiddleware")
	mono := indexOfType(names, "SubagentMonotoneMiddleware")
	spine := indexOfType(names, "SessionSpineMiddleware")
	require.Equal(t, 0, mode, "mode hint middleware must be outermost: %v", names)
	require.Less(t, gov, mono, names)
	require.Less(t, mono, spine, names)
	require.Equal(t, len(names)-1, spine, "spine must be the final context projection: %v", names)
	require.Less(t, indexOfType(names, "safeToolMiddleware"), indexOfType(names, "ADKToolHealMiddleware"), names)
	require.Less(t, indexOfType(names, "ADKToolHealMiddleware"), gov, names)
}
