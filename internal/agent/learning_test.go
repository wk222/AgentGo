package agent

import (
	"context"
	"testing"

	"agentgo/internal/memory"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestAfterAgentQueuesOnlyUserEvidenceWithoutForegroundModelCall(t *testing.T) {
	t.Setenv("AGENTGO_AUTO_LEARN", "1")
	t.Setenv("AGENTGO_AUTO_JOURNAL", "0")
	t.Setenv("AGENTGO_AUTO_REFLECT", "0")
	s, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	p := memory.NewPipeline(s)
	m := NewMemoryMiddleware(p)
	calls := 0
	m.SetJournalCaller(func(context.Context, string, string) (string, error) { calls++; return `{"atoms":[]}`, nil })
	ctx := WithSessionID(context.Background(), "office-session")
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("Please write concise reports with sources"), schema.AssistantMessage("I verified all policies and everything works", nil)}}
	for i := 0; i < 2; i++ {
		if _, err = m.AfterAgent(ctx, state); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatal("model on foreground path")
	}
	var text string
	err = s.DB().QueryRow(`SELECT content FROM memories WHERE json_extract(metadata,'$.source')='personal_learning_evidence'`).Scan(&text)
	if err != nil || text != "Please write concise reports with sources" {
		t.Fatal(text, err)
	}
	status, err := p.LearningStatus(ctx)
	if err != nil || status["pending"] != 1 {
		t.Fatal(status, err)
	}
	if err = p.ProcessLearning(ctx, 1); err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
}
func TestAfterAgentLearningDisabled(t *testing.T) {
	t.Setenv("AGENTGO_AUTO_LEARN", "0")
	s, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	p := memory.NewPipeline(s)
	m := NewMemoryMiddleware(p)
	_, err = m.AfterAgent(WithSessionID(context.Background(), "s"), &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("Please remember my reports must be concise"), schema.AssistantMessage("Understood", nil)}})
	if err != nil {
		t.Fatal(err)
	}
	status, err := p.LearningStatus(context.Background())
	if err != nil || status["pending"] != 0 {
		t.Fatal(status, err)
	}
}
