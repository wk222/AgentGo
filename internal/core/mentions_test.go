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

// A dropped file arrives as an @path with a Chinese name, brackets or spaces. The
// old pattern stopped at the first such character, so "@F:/工作簿1.xlsx" became
// "@F:/" and the model was shown a listing of the drive root instead.
func TestExpandMentionsKeepsWholeUnicodePaths(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	zip := []byte("PK\x03\x04\x14\x00\x06\x00binary-xlsx-bytes")
	inside := filepath.Join(root, "数据", "月度指标(1).xlsx")
	outside := filepath.Join(other, "工作簿1.xlsx")
	spaced := filepath.Join(other, "my report.docx")
	for _, p := range []string{inside, outside, spaced} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, zip, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "说明.txt"), []byte("纯文本内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := MentionSources{Root: root}
	noJunk := func(t *testing.T, got string) {
		t.Helper()
		if strings.Contains(got, "binary-xlsx-bytes") {
			t.Fatalf("a binary document must never be pasted into the prompt:\n%s", got)
		}
		if strings.Contains(got, "Context Directory") {
			t.Fatalf("the path was cut short and resolved to a folder:\n%s", got)
		}
	}

	got := ExpandMentions("@数据/月度指标(1).xlsx 分析下", src)
	noJunk(t, got)
	if !strings.Contains(got, "数据/月度指标(1).xlsx") || !strings.Contains(got, "read_xlsx") || !strings.Contains(got, "analyze_xlsx") {
		t.Fatalf("the hint must name the file and the xlsx tools:\n%s", got)
	}
	if strings.Contains(got, "工作区之外") {
		t.Fatalf("a file inside the workspace needs no outside-the-workspace note:\n%s", got)
	}

	got = ExpandMentions("@"+filepath.ToSlash(outside)+" 分析下这个文件", src)
	noJunk(t, got)
	for _, want := range []string{filepath.ToSlash(outside), "read_xlsx", "工作区之外", "审批"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}

	got = ExpandMentions(`看下 @"`+filepath.ToSlash(spaced)+`" 的内容`, src)
	noJunk(t, got)
	if !strings.Contains(got, "my report.docx") || !strings.Contains(got, "parse_file") {
		t.Fatalf("a quoted path with a space must resolve to the docx hint:\n%s", got)
	}

	// Text with a Chinese name is still pasted in, and Chinese punctuation or a
	// closing bracket right after the name is not part of it.
	for _, text := range []string{"请看@说明.txt，再说", "（见@说明.txt）", "(见@说明.txt)"} {
		if got := ExpandMentions(text, src); !strings.Contains(got, "纯文本内容") {
			t.Fatalf("%q must expand the text file:\n%s", text, got)
		}
	}
}

func TestExpandMentionsNeverPastesBinaryOfUnknownType(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blob.dat"), []byte("head\x00\x01\x02tail-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ExpandMentions("@blob.dat", MentionSources{Root: dir})
	if strings.Contains(got, "tail-bytes") || !strings.Contains(got, "blob.dat") {
		t.Fatalf("a NUL byte marks a binary file: name it, do not paste it:\n%s", got)
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
