package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentgo/internal/acpclient"
)

// The sessions an earlier run left with other coding agents live in the data directory, and only
// while the runtime that owns that directory is up.
func TestExternalAgentSessionsFollowTheDataDir(t *testing.T) {
	dir := t.TempDir()
	saved := fmt.Sprintf(`{"version":1,"sessions":[{"label":"review","agent":"claude","cwd":%q,"session_id":"abc","updated":%q}]}`,
		filepath.ToSlash(dir), time.Now().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dir, "external_agents.json"), []byte(saved), 0o600); err != nil {
		t.Fatal(err)
	}

	rt := bootIn(t, dir, nil)
	got := acpclient.Default.List()
	if len(got) != 1 || got[0].Label != "review" || got[0].Live || got[0].Agent != "claude" {
		t.Fatalf("a booted runtime must offer the sessions saved in its data directory: %+v", got)
	}

	_ = rt.Close()
	if got := acpclient.Default.List(); len(got) != 0 {
		t.Fatalf("a stopped runtime must not leave its store behind for the next one: %+v", got)
	}
}
