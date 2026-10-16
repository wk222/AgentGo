package bridge

import (
	"agentgo/internal/core"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestShellCommandChangesPopulateModifiedFiles(t *testing.T) {
	var path string
	_, files := runTurnFiles(t, func(c *crushTurn, dir string) {
		path = filepath.Join(dir, "s.txt")
		write(t, path, "before\n")
		c.toolCall("b1", core.ShellTool, `{"command":"sed -i ..."}`)
		write(t, path, "after\n") // the command runs
		c.toolResult("b1", core.ShellTool, "", false)
	})
	if len(files) != 2 || files[0].Content != "before\n" || files[1].Content != "after\n" || files[0].Path != path {
		t.Fatalf("files = %+v", files)
	}
}
