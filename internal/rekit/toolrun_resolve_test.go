package rekit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The catalog no longer names folders that belong to one person's machine (ilspycmd and idat
// used to). A tool installed somewhere unusual is found through RE_TOOL_<NAME> or tools.json,
// so these tests pin both ways, using a made-up tool that cannot be on anyone's PATH.

var demoTool = ToolSpec{Name: "zz-demo-tool", Exes: []string{"zz-demo-tool.exe", "zz-demo-tool"}}

func fakeExe(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("not a real program"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveFindsAToolPointedAtByAnEnvironmentVariable(t *testing.T) {
	exe := fakeExe(t, "zz-demo-tool.exe")
	tb := NewToolBox(t.TempDir(), false)
	if got := tb.resolve(demoTool); got != "" {
		t.Fatalf("a tool nobody configured resolved to %q", got)
	}
	if envKey(demoTool.Name) != "RE_TOOL_ZZ_DEMO_TOOL" {
		t.Fatalf("the variable for %q is %q; the docs and the not-found message name RE_TOOL_<NAME>", demoTool.Name, envKey(demoTool.Name))
	}
	t.Setenv(envKey(demoTool.Name), exe)
	if got := tb.resolve(demoTool); got != exe {
		t.Fatalf("resolve = %q, want the file the variable names (%q)", got, exe)
	}
}

func TestResolveFindsAToolListedInToolsJSON(t *testing.T) {
	exe := fakeExe(t, "zz-demo-tool.exe")
	home := t.TempDir()
	body, err := json.Marshal(map[string]string{demoTool.Name: exe})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "tools.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := NewToolBox(home, false).resolve(demoTool); got != exe {
		t.Fatalf("resolve = %q, want the file tools.json names (%q)", got, exe)
	}
}

func TestResolveIgnoresAnOverrideThatPointsAtNothing(t *testing.T) {
	// A stale variable must not hide the tool when it is also on PATH or in a conventional folder.
	exe := fakeExe(t, "zz-demo-tool.exe")
	spec := demoTool
	spec.Dirs = []string{filepath.Dir(exe)}
	t.Setenv(envKey(spec.Name), filepath.Join(t.TempDir(), "gone.exe"))
	if got := NewToolBox(t.TempDir(), false).resolve(spec); got != exe {
		t.Fatalf("resolve = %q, want the conventional-folder hit %q", got, exe)
	}
}
