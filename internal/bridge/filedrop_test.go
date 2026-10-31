package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDroppedFilesPayloadFlagsFoldersAndDropsNoise(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "全光提速实时通报2610(1)(1).xlsx")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "gone.docx")

	got, ok := droppedFilesPayload([]string{file, "  ", "", file, dir, missing})["files"].([]DroppedPath)
	if !ok {
		t.Fatal("files must be a []DroppedPath")
	}
	if len(got) != 3 {
		t.Fatalf("blank and repeated paths are dropped: %+v", got)
	}
	want := []DroppedPath{{file, false}, {dir, true}, {missing, false}}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDroppedFilesPayloadWithNothingIsAnEmptyList(t *testing.T) {
	got, ok := droppedFilesPayload(nil)["files"].([]DroppedPath)
	if !ok || got == nil || len(got) != 0 {
		t.Fatalf("an empty drop must serialise as [] not null: %#v", got)
	}
}
