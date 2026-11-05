package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentgo/internal/governance"
)

type fakeConversations struct {
	from, name, expert string
	created            int
}

func (f *fakeConversations) CreateConversation(_ context.Context, from, name, expert string) (any, error) {
	if expert == "nobody" {
		return nil, errors.New(`没有叫 "nobody" 的专家。现有专家：software-engineer（软件工程师）`)
	}
	f.from, f.name, f.expert = from, name, expert
	f.created++
	return map[string]any{"id": "sess_new", "title": name}, nil
}

func (f *fakeConversations) ListExperts(context.Context) (any, error) {
	return []map[string]string{{"id": "software-engineer", "name": "软件工程师"}}, nil
}

func newConversationTools(t *testing.T) (*Registry, *fakeConversations) {
	t.Helper()
	r := NewRegistry()
	if err := RegisterConversationTools(r); err != nil {
		t.Fatal(err)
	}
	f := &fakeConversations{}
	SetConversationHandler(f)
	t.Cleanup(func() { SetConversationHandler(nil) })
	return r, f
}

func TestConversationCreateUsesTheCallersSession(t *testing.T) {
	r, f := newConversationTools(t)
	ctx := governance.WithSessionID(context.Background(), "sess-A")
	out := invokeByName(t, r, ctx, "conversation_create", `{"name":"  季度预算  ","expert":" financial-modeler "}`)
	if !strings.Contains(out, `"success":true`) || !strings.Contains(out, "sess_new") {
		t.Fatalf("out=%s", out)
	}
	if f.from != "sess-A" || f.name != "季度预算" || f.expert != "financial-modeler" {
		t.Fatalf("from=%q name=%q expert=%q", f.from, f.name, f.expert)
	}
	// The answer must point at the next step, not leave the model to guess it.
	if !strings.Contains(out, "session_message") {
		t.Fatalf("the answer must say how to give the conversation work: %s", out)
	}
}

func TestConversationCreateRejectsBadInput(t *testing.T) {
	r, f := newConversationTools(t)
	ctx := governance.WithSessionID(context.Background(), "s")
	if out := invokeByName(t, r, ctx, "conversation_create", `{"name":"   "}`); strings.Contains(out, `"success":true`) || !strings.Contains(out, `"error"`) {
		t.Errorf("an empty name must be refused: %s", out)
	}
	out := invokeByName(t, r, ctx, "conversation_create", `{"name":"x","expert":"nobody"}`)
	if strings.Contains(out, `"success":true`) || !strings.Contains(out, "software-engineer") {
		t.Errorf("the host's reason, with the list of experts, must reach the model: %s", out)
	}
	if f.created != 0 {
		t.Errorf("a refused call must create nothing, created %d", f.created)
	}
}

func TestListExpertsToolReturnsTheHostsList(t *testing.T) {
	r, _ := newConversationTools(t)
	out := invokeByName(t, r, context.Background(), "list_experts", `{}`)
	if !strings.Contains(out, `"success":true`) || !strings.Contains(out, "software-engineer") {
		t.Fatalf("out=%s", out)
	}
}

func TestConversationToolsWithoutHost(t *testing.T) {
	r := NewRegistry()
	if err := RegisterConversationTools(r); err != nil {
		t.Fatal(err)
	}
	SetConversationHandler(nil)
	for name, args := range map[string]string{"conversation_create": `{"name":"x"}`, "list_experts": `{}`} {
		if out := invokeByName(t, r, context.Background(), name, args); !strings.Contains(out, `"error"`) || strings.Contains(out, `"success":true`) {
			t.Errorf("%s without a host must fail cleanly: %s", name, out)
		}
	}
}
