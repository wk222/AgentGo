//go:build windows

package computeruse

import (
	"os"
	"strings"
	"testing"
)

// TestLiveUIATree walks the real foreground window (read-only). Gated because
// it depends on whatever is on screen.
func TestLiveUIATree(t *testing.T) {
	if os.Getenv("AGENTGO_CUA_LIVE") != "1" {
		t.Skip("set AGENTGO_CUA_LIVE=1 to walk the real foreground window")
	}
	title, nodes, err := NewAccessibility().Tree(UITarget{Window: os.Getenv("AGENTGO_CUA_WINDOW")}, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) < 2 {
		t.Fatalf("window %q exposed only %d node(s)", title, len(nodes))
	}
	text := formatTree(title, nodes, nil)
	end := min(len(text), 2500)
	t.Logf("window %q, %d nodes\n%s", title, len(nodes), text[:end])
	if nodes[0].ID != 0 || nodes[0].Depth != 0 {
		t.Fatalf("root must be id 0 depth 0: %+v", nodes[0])
	}
}

func TestResolveWindowRejectsUnknownTitle(t *testing.T) {
	_, _, err := resolveWindow("zz-no-such-window-title-zz")
	if err == nil || !strings.Contains(err.Error(), "no visible window") {
		t.Fatalf("got %v", err)
	}
}

// TestLiveUIAReadByID re-finds an element by its id (read-only) to prove the
// walk is deterministic between Tree and Act.
func TestLiveUIAReadByID(t *testing.T) {
	if os.Getenv("AGENTGO_CUA_LIVE") != "1" {
		t.Skip("set AGENTGO_CUA_LIVE=1")
	}
	ax := NewAccessibility()
	_, nodes, err := ax.Tree(UITarget{}, 5)
	if err != nil || len(nodes) < 3 {
		t.Skipf("not enough nodes: %v", err)
	}
	pick := nodes[len(nodes)/2]
	id := pick.ID
	got, _, err := ax.Act(UITarget{}, 5, UISelector{ID: &id}, UIRead, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != pick.Name || got.Role != pick.Role {
		t.Fatalf("id %d resolved to a different element: %+v vs %+v", id, got, pick)
	}
}
