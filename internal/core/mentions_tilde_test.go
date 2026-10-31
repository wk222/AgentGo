package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/linktest"
)

// "~" belongs to a Windows short name (C:\Users\RUNNER~1\...), the way a
// temporary or attachment folder is often spelled, so a mention must keep it.
func TestExpandMentionsKeepsATildeInAPathButNotAtItsEnd(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "REPORT~1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "说明.txt"), []byte("短名目录里的文本"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("plain notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := MentionSources{Root: root}

	if got := ExpandMentions("@REPORT~1/说明.txt 看看", src); !strings.Contains(got, "短名目录里的文本") {
		t.Fatalf("a path with a tilde must stay whole:\n%s", got)
	}
	if got := ExpandMentions("@"+filepath.ToSlash(filepath.Join(dir, "说明.txt"))+" 看看", src); !strings.Contains(got, "短名目录里的文本") {
		t.Fatalf("an absolute path with a tilde must stay whole:\n%s", got)
	}
	// A tilde after the name is chatter, not a part of it.
	if got := ExpandMentions("看下@notes.txt~", src); !strings.Contains(got, "plain notes") {
		t.Fatalf("a trailing tilde is not part of the name:\n%s", got)
	}
}

func TestOutsideRootSeesThroughALink(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "root-link")
	linktest.Dir(t, link, root)

	if outsideRoot(link, filepath.Join(root, "a.txt")) {
		t.Error("a file of the workspace reached through a link is not outside it")
	}
	if outsideRoot(root, filepath.Join(link, "sub", "a.txt")) {
		t.Error("a file named through a link to the workspace is not outside it")
	}
	if !outsideRoot(link, filepath.Join(t.TempDir(), "a.txt")) {
		t.Error("a file of another folder is outside")
	}
	if outsideRoot("", filepath.Join(t.TempDir(), "a.txt")) {
		t.Error("with no root known there is nothing to be outside of")
	}
}
