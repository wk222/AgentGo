package core

import (
	"strings"
	"testing"
)

func TestExpandReviewKeepsTheTypedLineFirst(t *testing.T) {
	got := ExpandCommand("/review 并发安全")
	first, rest, _ := strings.Cut(got, "\n")
	if first != "/review 并发安全" {
		t.Fatalf("first line = %q, want what the user typed", first)
	}
	if !strings.HasPrefix(strings.TrimSpace(rest), `<command-instructions name="review">`) {
		t.Fatalf("instructions block missing after the typed line:\n%s", got)
	}
	if !strings.HasSuffix(got, "</command-instructions>") {
		t.Fatalf("instructions block is not closed:\n%s", got)
	}
	for _, want := range []string{
		"get_workspace_changes", // where the diff comes from
		"?? 开头",                 // new files are not in the diff
		"untracked",             // ... their content comes with the tool result
		"改动之前就存在的问题不报",          // only what the change introduced
		"改名、调格式",                // even when the change touched the old problem
		"P0", "P3",              // severity scale
		"不要修改任何文件", // review is read-only
		"审查重点（优先看这些，但不排斥发现其他问题）：并发安全",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("review prompt lacks %q", want)
		}
	}
}

func TestExpandReviewWithoutFocusHasNoFocusLine(t *testing.T) {
	got := ExpandCommand("  /review  ")
	if strings.Contains(got, "审查重点") {
		t.Fatalf("a bare /review must not mention a focus:\n%s", got)
	}
	if first, _, _ := strings.Cut(got, "\n"); first != "/review" {
		t.Fatalf("first line = %q", first)
	}
}

func TestExpandReviewIsCaseInsensitiveAndKeepsMultilineFocus(t *testing.T) {
	got := ExpandCommand("/Review 看 @a.go\n再看错误处理")
	if !strings.Contains(got, "审查重点（优先看这些，但不排斥发现其他问题）：看 @a.go\n再看错误处理") {
		t.Fatalf("focus lost:\n%s", got)
	}
	// The typed text is kept verbatim at the top, so @a.go still expands later.
	if !strings.HasPrefix(got, "/Review 看 @a.go\n再看错误处理\n\n<command-instructions") {
		t.Fatalf("typed text changed:\n%s", got)
	}
}

func TestExpandCommandLeavesOtherTextAlone(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"请 review 一下这个改动",
		"/review/src/app.go 里的逻辑", // a path, not the command
		"/reviewer",               // a different word
		"/unknown 参数",             // not a command we know
		"/",                       // nothing after the slash
		"看看 /review 这个命令怎么用",      // not at the start
	} {
		if got := ExpandCommand(in); got != in {
			t.Errorf("ExpandCommand(%q) = %q, want the input unchanged", in, got)
		}
	}
}

func TestExpandCommandDoesNotExpandTwice(t *testing.T) {
	once := ExpandCommand("/review")
	if twice := ExpandCommand(once); twice != once {
		t.Fatalf("second expansion changed the text:\n%s", twice)
	}
}

func TestExpandUndoNamesTheTools(t *testing.T) {
	got := ExpandCommand("/undo")
	for _, want := range []string{"/undo", "code_file_changes", "code_revert_file_changes", "call_id 留空", `name="undo"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if all := ExpandCommand("/undo all"); !strings.Contains(all, `call_id 填 "all"`) {
		t.Fatalf("/undo all: %q", all)
	}
	if one := ExpandCommand("/undo 撤销 b.txt 那次"); !strings.Contains(one, "撤销 b.txt 那次") || strings.Contains(one, "call_id 留空") {
		t.Fatalf("/undo with a scope: %q", one)
	}
}
