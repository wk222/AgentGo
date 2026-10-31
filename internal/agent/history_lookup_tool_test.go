package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHistoryLookupRecoversOriginalToolOutputWithUnicodePagination(t *testing.T) {
	ctx := context.Background()
	s, _ := testRuntimeStore(t, filepath.Join(t.TempDir(), "history.db"))
	full := "HEAD " + strings.Repeat("中文🐈", 3000) + " TAIL"
	event := &adk.SessionEvent[*schema.Message]{EventID: "original", Kind: adk.SessionEventMessage, Message: schema.ToolMessage(full, "call")}
	if err := s.AppendEvents(ctx, "a", []*adk.SessionEvent[*schema.Message]{event}); err != nil {
		t.Fatal(err)
	}
	replacement := []*schema.Message{schema.UserMessage("compressed; detail omitted")}
	if err := s.AppendEvents(ctx, "a", []*adk.SessionEvent[*schema.Message]{{EventID: "compact", Kind: adk.SessionEventMessagesReplaced, MessagesReplaced: &replacement}}); err != nil {
		t.Fatal(err)
	}
	page, err := lookupSessionHistory(ctx, s, "a", historyLookupInput{Query: "HEAD", Limit: 1})
	if err != nil || len(page.Items) != 0 || page.NextCursor == "" {
		t.Fatalf("empty filtered page lost cursor: %+v %v", page, err)
	}
	page, err = lookupSessionHistory(ctx, s, "a", historyLookupInput{Query: "HEAD", Limit: 1, Before: page.NextCursor})
	if err != nil || len(page.Items) != 1 || page.Items[0].RecordID != "original" {
		t.Fatalf("original unavailable: %+v %v", page, err)
	}
	offset := 0
	var recovered strings.Builder
	for {
		p, err := lookupSessionHistory(ctx, s, "a", historyLookupInput{RecordID: "original", Offset: offset, MaxChars: 997})
		if err != nil {
			t.Fatal(err)
		}
		item := p.Items[0]
		if !utf8.ValidString(item.Text) || len([]rune(item.Text)) > 997 {
			t.Fatal("invalid text slice")
		}
		recovered.WriteString(item.Text)
		if item.NextOffset == 0 {
			break
		}
		offset = item.NextOffset
	}
	if recovered.String() != full {
		t.Fatal("archive paging lost or duplicated original bytes")
	}
	if _, err := lookupSessionHistory(ctx, s, "b", historyLookupInput{RecordID: "original"}); err == nil {
		t.Fatal("cross-session archive access")
	}
	other := newRuntimeSessionStore(s.store, t.TempDir())
	if _, err := lookupSessionHistory(ctx, other, "a", historyLookupInput{RecordID: "original"}); err == nil {
		t.Fatal("cross-workspace runtime access")
	}
}
func TestHistoryToolReadsOlderTranscriptAndRejectsForeignCursors(t *testing.T) {
	ctx := context.Background()
	s, _ := testRuntimeStore(t, filepath.Join(t.TempDir(), "legacy.db"))
	for _, sid := range []string{"a", "b"} {
		if err := s.store.EnsureSession(ctx, sid); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 80; i++ {
		text := fmt.Sprintf("old constraint %d", i)
		if i == 0 {
			text = "FIRST-USER-CONSTRAINT"
		}
		if err := s.store.AppendMessage(ctx, "a", "user", text, "text", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.AppendMessage(ctx, "b", "user", "FOREIGN-SECRET", "text", nil); err != nil {
		t.Fatal(err)
	}
	tool, err := newSessionHistoryTool(s, "agentic:a")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tool.InvokableRun(ctx, "{\"source\":\"transcript\",\"query\":\"FIRST-USER-CONSTRAINT\"}")
	if err != nil {
		t.Fatal(err)
	}
	var page historyLookupResult
	if err = json.Unmarshal([]byte(raw), &page); err != nil || len(page.Items) != 1 || !strings.Contains(page.Items[0].Text, "FIRST-USER-CONSTRAINT") {
		t.Fatalf("older-than-window text unavailable: %s %v", raw, err)
	}
	p, err := lookupSessionHistory(ctx, s, "b", historyLookupInput{Source: "transcript", Query: "FOREIGN-SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	foreign := p.Items[0].RecordID
	for _, input := range []historyLookupInput{{Source: "transcript", RecordID: foreign}, {Source: "transcript", Before: foreign}} {
		if _, err := lookupSessionHistory(ctx, s, "a", input); err == nil {
			t.Fatal("foreign record/cursor accepted")
		}
	}
	raw, err = tool.InvokableRun(ctx, "{\"source\":\"transcript\",\"query\":\"FOREIGN-SECRET\",\"session_id\":\"b\"}")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "FOREIGN-SECRET") {
		t.Fatal("argument overrode bound conversation")
	}
}
