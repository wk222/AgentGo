package sessions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestSessionKernelEnrichSnapshot(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "TEAM.md"), []byte("team alpha"), 0644)
	k := NewSessionKernel(dir, 5, 10)
	k.RecordFileTouch("src/main.go")
	snap := k.EnrichSnapshot(SpineSnapshot{}, []*schema.Message{
		schema.UserMessage("hi"),
		schema.AssistantMessage("ok", nil),
	})
	if len(snap.TeamMemory) == 0 {
		t.Fatal("expected team memory from TEAM.md")
	}
	if len(snap.WorkspaceRecent) == 0 {
		t.Fatal("expected workspace recent")
	}
}
