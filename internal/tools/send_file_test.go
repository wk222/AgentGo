package tools

import (
	"context"
	"strings"
	"testing"
)

func TestSendFileWithoutIMChannel(t *testing.T) {
	r := NewRegistry()
	if err := RegisterSendFileTool(r, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	out := invokeByName(t, r, context.Background(), "send_file", `{"path":"report.txt"}`)
	if !strings.Contains(out, "no IM channel") || strings.Contains(out, `"success":true`) {
		t.Fatalf("unexpected output: %s", out)
	}
}
