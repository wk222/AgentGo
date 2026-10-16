package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandMentionsFileCodebaseAndPassThrough(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("Hello Mentions!"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := MentionSources{Root: dir}

	if got := ExpandMentions("no mention here", src); got != "no mention here" {
		t.Fatalf("untouched text expected, got %q", got)
	}
	if got := ExpandMentions("see @sample.txt", src); !strings.Contains(got, "Context File: `sample.txt`") || !strings.Contains(got, "Hello Mentions!") {
		t.Fatalf("file block missing: %s", got)
	}
	if got := ExpandMentions("about @Codebase", src); !strings.Contains(got, "Codebase Overview") {
		t.Fatalf("codebase block missing: %s", got)
	}
	// Unknown targets and escapes out of the root add nothing.
	if got := ExpandMentions("x @nope.txt y", src); got != "x @nope.txt y" {
		t.Fatalf("unknown file must not expand: %s", got)
	}
}

func TestExpandMentionsTerminalAndDiagnostics(t *testing.T) {
	src := MentionSources{
		Root:     t.TempDir(),
		Terminal: func() (string, string, bool) { return "pwsh", "build failed", true },
		Diagnostics: func() (map[string]int, func(string) any, bool) {
			return map[string]int{"gopls": 2, "tsserver": 0}, func(n string) any { return "details of " + n }, true
		},
	}
	got := ExpandMentions("@Terminal and @Problems", src)
	for _, want := range []string{"Terminal Output (pwsh)", "build failed", "Server `gopls` reported 2 issue(s)", "details of gopls"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "tsserver") {
		t.Fatalf("a server with no issues must not be listed:\n%s", got)
	}

	// Hosts without terminals or language servers degrade, they do not fail.
	bare := ExpandMentions("@Terminal @Problems", MentionSources{Root: t.TempDir()})
	if !strings.Contains(bare, "not initialized") || strings.Contains(bare, "Terminal Output") {
		t.Fatalf("bare host: %s", bare)
	}
}
