package codetools

import (
	"context"
	"sort"
	"testing"

	ct "github.com/charmbracelet/crush/pkg/codetools"

	"agentgo/internal/governance"
)

// The governance risk table is keyed by tool name, while the toolbox decides
// what tools exist. This guards the seam between them: every file-changing
// tool the toolbox ships must be stopped for approval by the default policy.
// codetools' own Approver is nil in AgentGo (allow everything), so governance
// is the only gate and a gap here means silent writes.

// newToolbox returns a toolbox whose traits are registered the way the plugin
// does it, and withdrawn again when the test ends.
func newToolbox(t *testing.T) *ct.Toolbox {
	t.Helper()
	tb, err := ct.New(ct.Options{WorkDir: t.TempDir(), DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tb.Close)
	t.Cleanup(RegisterTraits(tb.Tools))
	return tb
}

// ranWithoutApproval reports whether the call went straight through.
func ranWithoutApproval(t *testing.T, mode, tool string) bool {
	t.Helper()
	queue, err := governance.NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })
	mw := governance.NewGovernanceMiddleware(queue, governance.BuildPolicy(mode, t.TempDir()))
	ran := false
	_, err = mw.InvokeWithPolicy(context.Background(), tool, `{}`, func(context.Context, string) (string, error) {
		ran = true
		return "ok", nil
	})
	_ = err // an interrupt/deny error is the expected outcome for gated tools
	return ran
}

func TestMutatingCodeToolsNeedApprovalUnderDefaultPolicies(t *testing.T) {
	tb := newToolbox(t)
	for _, mode := range []string{"balanced", "strict"} {
		for _, tool := range tb.Tools {
			if !IsMutating(tool.Name) {
				continue
			}
			name := ToolName(tool.Name)
			if ranWithoutApproval(t, mode, name) {
				t.Errorf("%s policy: %s changes files but ran without approval", mode, name)
			}
		}
	}
}

func TestReadOnlyCodeToolsAreNotGated(t *testing.T) {
	tb := newToolbox(t)
	for _, tool := range tb.Tools {
		if IsMutating(tool.Name) {
			continue
		}
		name := ToolName(tool.Name)
		if !ranWithoutApproval(t, "balanced", name) {
			t.Errorf("%s is read-only but is being held for approval", name)
		}
	}
}

// New Crush tools must be classified on purpose: if the toolbox grows, this
// fails until IsMutating and the governance risk table have been looked at.
func TestCodeToolSetIsKnown(t *testing.T) {
	tb := newToolbox(t)
	var got []string
	for _, tool := range tb.Tools {
		got = append(got, tool.Name)
	}
	sort.Strings(got)
	// lsp_rename / lsp_replace_symbol are in IsMutating and the risk table in
	// case the toolbox starts shipping them; today it does not.
	want := []string{
		"edit", "glob", "grep", "ls", "lsp_call_hierarchy", "lsp_definition", "lsp_diagnostics",
		"lsp_references", "lsp_symbols", "multiedit", "view", "write",
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("toolbox tools = %v, want %v; classify the new tool in IsMutating and the risk table", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("toolbox tools = %v, want %v; classify the new tool in IsMutating and the risk table", got, want)
		}
	}
}
