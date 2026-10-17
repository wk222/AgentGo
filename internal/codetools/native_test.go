package codetools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
)

// Parity (plan-crush-digestion D2): the native file tools must tell the model
// exactly what the Crush ones did for the same calls, and leave the same bytes
// on disk. The scenario below is run against both and the transcripts compared.

type step struct {
	tool string
	args func(dir string) string
}

func jsonPath(dir, name string) string { return quote(filepath.Join(dir, name)) }

// runToolLoose returns the tool's output, or its error text: the model sees
// both, so both are compared.
func runToolLoose(t *testing.T, tools map[string]einotool.BaseTool, name, args string) string {
	t.Helper()
	tl, ok := tools[name].(invokable)
	if !ok {
		t.Fatalf("tool %s missing or not invokable", name)
	}
	out, err := tl.InvokableRun(context.Background(), args)
	if err != nil {
		return "ERR: " + err.Error()
	}
	return out
}

// stripFailedEditLines removes the one deliberate difference: native multiedit
// lists each failed edit's reason in the result, where Crush only counted them
// (the reasons sat in metadata the model never saw).
func stripFailedEditLines(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "edit ") && strings.Contains(l, " failed: ") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

func parityScenario() []step {
	view := func(name, extra string) step {
		return step{"code_view", func(d string) string { return `{"file_path":` + jsonPath(d, name) + extra + `}` }}
	}
	edit := func(name, old, new string, all bool) step {
		return step{"code_edit", func(d string) string {
			a := `{"file_path":` + jsonPath(d, name) + `,"old_string":` + quote(old) + `,"new_string":` + quote(new)
			if all {
				a += `,"replace_all":true`
			}
			return a + `}`
		}}
	}
	write := func(name, content string) step {
		return step{"code_write", func(d string) string {
			return `{"file_path":` + jsonPath(d, name) + `,"content":` + quote(content) + `}`
		}}
	}
	multi := func(name string, ops ...[2]string) step {
		return step{"code_multiedit", func(d string) string {
			var parts []string
			for _, o := range ops {
				parts = append(parts, `{"old_string":`+quote(o[0])+`,"new_string":`+quote(o[1])+`}`)
			}
			return `{"file_path":` + jsonPath(d, name) + `,"edits":[` + strings.Join(parts, ",") + `]}`
		}}
	}
	return []step{
		// read-before-modify
		edit("a.go", "alpha", "ALPHA", false),
		write("a.go", "overwritten\n"),
		view("a.go", ""),
		// plain, ambiguous, missing
		edit("a.go", "alpha", "ALPHA", false),
		edit("a.go", "dup", "DUP", false),
		edit("a.go", "dup", "DUP", true),
		edit("a.go", "does not exist anywhere", "x", false),
		edit("a.go", "ALPHA", "ALPHA", false),
		// whitespace-tolerant match and hint
		view("ws.go", ""),
		edit("ws.go", "if x {\n    return 1\n}", "if x {\n    return 2\n}", false),
		edit("ws.go", "if y {\nreturn 9\n}", "z", false),
		view("ws2.go", ""),
		edit("ws2.go", "  x := 1", "z := 3", false),
		// delete and create
		edit("a.go", "line three\n", "", false),
		edit("a.go", "", "brand new\n", false),
		edit("new/dir/c.txt", "", "created\n", false),
		view("a.go", ""),
		// pagination
		view("long.txt", `,"offset":5,"limit":3`),
		view("long.txt", `,"offset":500`),
		view("missing.txt", ""),
		view("a", ""),
		// multiedit with one failing edit
		view("m.txt", ""),
		multi("m.txt", [2]string{"one", "1"}, [2]string{"nope", "x"}, [2]string{"three", "3"}),
		multi("m.txt", [2]string{"nope", "x"}),
		multi("fresh.txt", [2]string{"", "seed\n"}, [2]string{"seed", "grown"}),
		// write after view, identical content
		view("w.txt", ""),
		write("w.txt", "changed\n"),
		write("w.txt", "changed\n"),
		// CRLF survives an edit
		view("crlf.txt", ""),
		edit("crlf.txt", "second", "SECOND", false),
	}
}

func seedParity(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"a.go":     "alpha\ndup\nline three\ndup\n",
		"ws.go":    "func f() {\n\tif x {\n\t\treturn 1\n\t}\n}\n",
		"ws2.go":   "\tx := 1\n\ty := 2\n\tx := 1\n",
		"m.txt":    "one\ntwo\nthree\n",
		"w.txt":    "original\n",
		"crlf.txt": "first\r\nsecond\r\nthird\r\n",
	}
	var long strings.Builder
	for i := 1; i <= 20; i++ {
		long.WriteString("row\n")
	}
	files["long.txt"] = long.String()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func runParity(t *testing.T, native bool) (transcript []string, disk map[string]string) {
	t.Helper()
	dir, tools := baselineTools(t, native)
	seedParity(t, dir)
	for _, s := range parityScenario() {
		out := runToolLoose(t, tools, s.tool, s.args(dir))
		if native {
			out = stripFailedEditLines(out)
		}
		transcript = append(transcript, strings.ReplaceAll(out, dir, "<D>"))
	}
	disk = map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(dir, p)
			disk[filepath.ToSlash(rel)] = string(b)
		}
		return nil
	})
	return transcript, disk
}

func TestNativeFSParityWithCrush(t *testing.T) {
	wantT, wantD := runParity(t, false)
	gotT, gotD := runParity(t, true)

	// The scenario must really exercise the paths, not pass on a wall of errors.
	all := strings.Join(wantT, "\n")
	for _, marker := range []string{
		"you must read the file before editing", "has been modified since it was last read",
		"Content replaced in file", "Content deleted from file", "File created:",
		"appears multiple times", "old_string not found", "whitespace-equivalent",
		"A whitespace-normalized match", "File has more lines", "File not found",
		"is a directory", "Applied 2 of 3 edits", "File created with", "already contains the exact content",
	} {
		if !strings.Contains(all, marker) {
			t.Errorf("scenario never produced %q", marker)
		}
	}

	if len(gotT) != len(wantT) {
		t.Fatalf("transcript length %d != %d", len(gotT), len(wantT))
	}
	names := parityScenario()
	for i := range wantT {
		if gotT[i] != wantT[i] {
			t.Errorf("step %d (%s) differs\n--- crush ---\n%s\n--- native ---\n%s", i, names[i].tool, wantT[i], gotT[i])
		}
	}
	for name, want := range wantD {
		if got, ok := gotD[name]; !ok || got != want {
			t.Errorf("disk %s differs\n--- crush ---\n%q\n--- native ---\n%q", name, want, got)
		}
	}
	for name := range gotD {
		if _, ok := wantD[name]; !ok {
			t.Errorf("native created extra file %s", name)
		}
	}
}
