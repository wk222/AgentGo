package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The sandbox folder is only named at startup (SetDynamicSandbox); nothing creates
// it. A fresh data directory therefore failed the first run of every generated tool
// with "cannot find the file specified" until the folder happened to exist.
func TestGeneratedToolRunsWhenTheSandboxFolderDoesNotExistYet(t *testing.T) {
	_, reg := newDynStoreAndRegistry(t)
	reg.SetDynamicSandbox(filepath.Join(t.TempDir(), "data", "sandbox"))
	def := DynamicToolDef{Name: "fresh_tool", Description: "d", Parameters: "{}", Code: "print('ran')"}
	if err := reg.RegisterDynamicTool(def); err != nil {
		t.Fatal(err)
	}
	out, err := reg.InvokeJSON(context.Background(), "fresh_tool", `{"arguments":"{}"}`)
	if err != nil || !strings.Contains(out, "ran") || strings.Contains(out, "cannot find") {
		t.Fatalf("out=%s err=%v", out, err)
	}
}
