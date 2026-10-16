package bridge

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"agentgo/internal/sessions"
)

func sm(role, typ, content string) sessions.Message {
	return sessions.Message{Role: role, Type: typ, Content: content}
}

func TestBuildHistoryKeepsOnlyChatTextOldestFirst(t *testing.T) {
	got := buildHistory([]sessions.Message{
		sm("user", "text", "q1"),
		sm("assistant", "text", "a1"),
		sm("assistant", "approval", "等待审批: write"), // not replayed
		sm("assistant", "question", "which?"),      // not replayed
		sm("tool", "text", "tool output"),          // not chat
		sm("assistant", "text", "   "),             // blank
		sm("user", "", "q2"),                       // legacy rows have an empty type
		sm("assistant", "text", "a2"),
	}, 24, 10000)
	var s []string
	for _, m := range got {
		s = append(s, string(m.Role)+":"+m.Content)
	}
	if want := "user:q1|assistant:a1|user:q2|assistant:a2"; strings.Join(s, "|") != want {
		t.Fatalf("history = %v, want %s", s, want)
	}
}

func TestBuildHistoryRespectsBudgetsAndStartsWithUser(t *testing.T) {
	var in []sessions.Message
	for i := 0; i < 10; i++ {
		in = append(in, sm("user", "text", "u"), sm("assistant", "text", "a"))
	}
	got := buildHistory(in, 5, 10000) // 5 newest = a u a u a -> leading assistant dropped
	if len(got) != 4 || got[0].Role != schema.User || got[len(got)-1].Role != schema.Assistant {
		t.Fatalf("history = %+v", got)
	}

	big := strings.Repeat("x", 600)
	got = buildHistory([]sessions.Message{
		sm("user", "text", big), sm("assistant", "text", big), sm("user", "text", "latest"), sm("assistant", "text", big),
	}, 24, 1000)
	// budget keeps the newest turns; the oldest big ones fall off
	total := 0
	for _, m := range got {
		total += len(m.Content)
	}
	if total > 1000+len("latest") || (len(got) > 0 && got[0].Role != schema.User) {
		t.Fatalf("budget/ordering violated: total=%d msgs=%d", total, len(got))
	}
	if buildHistory(nil, 5, 100) != nil {
		t.Fatal("empty input must give no history")
	}
}
