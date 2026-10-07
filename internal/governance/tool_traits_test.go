package governance

import (
	"context"
	"testing"
)

func runThrough(t *testing.T, mode, tool string) (ran bool) {
	t.Helper()
	queue, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })
	mw := NewGovernanceMiddleware(queue, BuildPolicy(mode, t.TempDir()))
	_, _ = mw.InvokeWithPolicy(context.Background(), tool, `{}`, func(context.Context, string) (string, error) {
		ran = true
		return "ok", nil
	})
	return ran
}

// A plugin tool the risk table has never heard of is "low" by default, and low
// calls skip approval entirely. Declaring Mutating must be enough to hold it.
func TestDeclaredMutatingToolNeedsApprovalWithoutAnyNameList(t *testing.T) {
	const name = "thirdparty_rewrite_file"
	if !runThrough(t, "balanced", name) {
		t.Fatal("precondition: an unknown, undeclared tool runs freely")
	}
	dispose := RegisterToolTraits(name, ToolTraits{Source: "test", Mutating: true})
	for _, mode := range []string{"balanced", "strict"} {
		if runThrough(t, mode, name) {
			t.Errorf("%s: a declared mutating tool ran without approval", mode)
		}
	}
	if !runThrough(t, "open", name) {
		t.Error("open mode intentionally does not hold file changes")
	}
	dispose()
	if !runThrough(t, "balanced", name) {
		t.Error("withdrawing the traits must lift the hold")
	}
}

func TestDeclaredReadOnlyToolIsNotHeld(t *testing.T) {
	const name = "thirdparty_read_file"
	t.Cleanup(RegisterToolTraits(name, ToolTraits{Source: "test", Mutating: false}))
	if !runThrough(t, "balanced", name) {
		t.Fatal("a read-only tool must not need approval")
	}
}

// Reloading a plugin registers the same names again before the old disposers
// run; the stale disposer must not erase the new registration.
func TestStaleTraitDisposerDoesNotEraseReRegistration(t *testing.T) {
	const name = "thirdparty_reload_tool"
	old := RegisterToolTraits(name, ToolTraits{Source: "v1", Mutating: true})
	fresh := RegisterToolTraits(name, ToolTraits{Source: "v2", Mutating: true})
	t.Cleanup(fresh)
	old()
	if tr, ok := ToolTraitsOf(name); !ok || tr.Source != "v2" {
		t.Fatalf("traits = %+v ok=%v, want v2 still registered", tr, ok)
	}
}
