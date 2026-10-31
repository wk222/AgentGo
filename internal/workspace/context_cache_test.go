package workspace

import (
	"context"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWorkspaceContextCacheInvalidation(t *testing.T) {
	root := t.TempDir()
	team := filepath.Join(root, "TEAM.md")
	_ = os.WriteFile(team, []byte("first"), 0600)
	m := NewWorkspaceManager(root)
	if !strings.Contains(m.BuildContext(), "first") {
		t.Fatal("missing rules")
	}
	reads := m.reads
	m.BuildContext()
	if m.reads != reads {
		t.Fatal("unchanged file reread")
	}
	_ = os.WriteFile(team, []byte("changed team"), 0600)
	if !strings.Contains(m.BuildContext(), "changed team") || m.reads != reads+1 {
		t.Fatal("stale edit")
	}
	rules := filepath.Join(root, ".cursor", "rules")
	_ = os.MkdirAll(rules, 0700)
	_ = os.WriteFile(filepath.Join(rules, "new.mdc"), []byte("new-rule"), 0600)
	if !strings.Contains(m.BuildContext(), "new-rule") {
		t.Fatal("new rule not detected")
	}
	_ = os.Remove(team)
	if strings.Contains(m.BuildContext(), "changed team") {
		t.Fatal("removed rule still injected")
	}
}
func TestWorkspaceContextIsBoundedUTF8AndIdempotent(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "TEAM.md"), []byte(strings.Repeat("中文🐈", 20000)), 0600)
	m := NewContextMiddleware(root)
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("q")}}
	for i := 0; i < 10; i++ {
		_, _, err := m.BeforeModelRewriteState(context.Background(), state, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(state.Messages) != 3 || state.Messages[1].Content != "BASE" {
		t.Fatal("rules accumulated or mutated base prompt")
	}
	text := m.manager.BuildContext()
	if !utf8.ValidString(text) || len(text) > maxWorkspaceContextBytes {
		t.Fatal("invalid or unbounded context")
	}
}

func BenchmarkWorkspaceContextCache(b *testing.B) {
	root := b.TempDir()
	_ = os.WriteFile(filepath.Join(root, "SOUL.md"), []byte(strings.Repeat("workspace instruction\n", 600)), 0600)
	_ = os.WriteFile(filepath.Join(root, "TEAM.md"), []byte(strings.Repeat("team instruction\n", 600)), 0600)
	b.Run("cached", func(b *testing.B) {
		m := NewWorkspaceManager(root)
		m.BuildContext()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			m.BuildContext()
		}
	})
	b.Run("cold", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			NewWorkspaceManager(root).BuildContext()
		}
	})
}
