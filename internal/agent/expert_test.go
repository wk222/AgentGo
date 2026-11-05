package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

const testExpertBlock = "[Expert: 财务建模师 (financial-modeler)]\nRules:\n先问假设，再建模。"

func systemAfterModelCalls(t *testing.T, ctx context.Context, calls int) string {
	t.Helper()
	mw := NewModeProfileMiddleware(DefaultSessionMode())
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("q")}}
	var err error
	for i := 0; i < calls; i++ {
		if _, state, err = mw.BeforeModelRewriteState(ctx, state, nil); err != nil {
			t.Fatal(err)
		}
	}
	return state.Messages[0].Content
}

func TestExpertRulesReachTheSystemPromptOnceAfterTheModeHints(t *testing.T) {
	r := &Runner{}
	var asked []string
	r.SetExpertResolver(func(id string) string {
		asked = append(asked, id)
		if id == "s1" {
			return "\n" + testExpertBlock + "\n"
		}
		return ""
	})
	ctx := r.withSessionMode(context.Background(), "s1")

	sys := systemAfterModelCalls(t, ctx, 1)
	if !strings.HasPrefix(sys, "BASE\n\n[ModeProfile:") {
		t.Fatalf("the mode guidance still follows the base prompt:\n%s", sys)
	}
	if n := strings.Count(sys, testExpertBlock); n != 1 {
		t.Fatalf("the expert block appears %d times:\n%s", n, sys)
	}
	if strings.Index(sys, "[Expert:") < strings.Index(sys, "[ModeProfile:") {
		t.Fatalf("the expert's rules come after the mode guidance:\n%s", sys)
	}
	// Every model call of a run goes through the middleware: nothing may pile up.
	if again := systemAfterModelCalls(t, ctx, 6); again != sys {
		t.Fatalf("the system prompt grew across model calls: %d -> %d chars", len(sys), len(again))
	}
	if len(asked) != 1 || asked[0] != "s1" {
		t.Fatalf("the resolver is asked once per run, about that run's session: %v", asked)
	}
}

func TestConversationsWithoutAnExpertAreUnchanged(t *testing.T) {
	r := &Runner{}
	plain := systemAfterModelCalls(t, r.withSessionMode(context.Background(), "s1"), 1)
	if strings.Contains(plain, "[Expert:") {
		t.Fatalf("no resolver, no expert:\n%s", plain)
	}

	r.SetExpertResolver(func(id string) string {
		if id == "with" {
			return testExpertBlock
		}
		return ""
	})
	if got := systemAfterModelCalls(t, r.withSessionMode(context.Background(), "without"), 1); got != plain {
		t.Fatalf("a conversation with no expert changed:\nwant %q\ngot  %q", plain, got)
	}
	if got := systemAfterModelCalls(t, r.withSessionMode(context.Background(), ""), 1); got != plain {
		t.Fatal("a run without a session id is not an expert's run")
	}
	r.SetExpertResolver(nil)
	if got := systemAfterModelCalls(t, r.withSessionMode(context.Background(), "with"), 1); got != plain {
		t.Fatal("removing the resolver removes the expert")
	}
}

// session_message and scheduled turns start a run inside another run's context.
// The conversation they act on has its own expert, or none: never the caller's.
func TestAnExpertDoesNotLeakIntoAnotherConversationsRun(t *testing.T) {
	r := &Runner{}
	r.SetExpertResolver(func(id string) string {
		if id == "outer" {
			return testExpertBlock
		}
		return ""
	})
	outer := r.withSessionMode(context.Background(), "outer")
	if expertBlockFromContext(outer) != testExpertBlock {
		t.Fatal("the outer run has its expert")
	}
	inner := r.withSessionMode(outer, "inner")
	if got := expertBlockFromContext(inner); got != "" {
		t.Fatalf("the inner conversation inherited %q", got)
	}
	if sys := systemAfterModelCalls(t, inner, 1); strings.Contains(sys, "[Expert:") {
		t.Fatalf("inner system prompt:\n%s", sys)
	}
}

func TestExpertBlockSurvivesAnExplicitModeInTheContext(t *testing.T) {
	r := &Runner{}
	r.SetExpertResolver(func(string) string { return testExpertBlock })
	ctx := WithSessionMode(context.Background(), SessionMode{Profile: ModeAdmin, Canvas: CanvasDeep})
	ctx = r.withSessionMode(ctx, "s1")
	if m := SessionModeFromContext(ctx); m.Profile != ModeAdmin {
		t.Fatalf("the explicit mode is kept: %+v", m)
	}
	if sys := systemAfterModelCalls(t, ctx, 2); !strings.Contains(sys, testExpertBlock) || !strings.Contains(sys, "[ModeProfile: admin") {
		t.Fatalf("both mode guidance and expert rules:\n%s", sys)
	}
}

func TestAgenticMiddlewareAddsTheExpertToo(t *testing.T) {
	r := &Runner{}
	r.SetExpertResolver(func(string) string { return testExpertBlock })
	ctx := r.withSessionMode(context.Background(), "s1")

	mw := NewAgenticModeProfileMiddleware(DefaultSessionMode())
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{Messages: []*schema.AgenticMessage{schema.SystemAgenticMessage("BASE")}}
	var err error
	for i := 0; i < 3; i++ {
		if _, state, err = mw.BeforeModelRewriteState(ctx, state, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := state.Messages[0].String(); strings.Count(got, testExpertBlock) != 1 {
		t.Fatalf("the expert block must be there exactly once:\n%s", got)
	}
}
