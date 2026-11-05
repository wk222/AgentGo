package sessions

import (
	"context"
	"testing"
)

func TestExpertBindingRoundTrip(t *testing.T) {
	s, _ := deletionStore(t)
	ctx := context.Background()
	sess, err := s.Create(ctx, "报告")
	if err != nil {
		t.Fatal(err)
	}

	if got, err := s.Expert(ctx, sess.ID); err != nil || got != "" {
		t.Fatalf("a new conversation has no expert: %q, %v", got, err)
	}
	if err := s.SetExpert(ctx, " "+sess.ID+" ", " financial-modeler "); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Expert(ctx, sess.ID); got != "financial-modeler" {
		t.Fatalf("expert = %q", got)
	}
	// Both ways of reading a session carry it, so the sidebar can show it.
	if row, ok := sessionRow(t, s, sess.ID); !ok || row.ExpertID != "financial-modeler" {
		t.Fatalf("List: %+v", row)
	}
	if one, err := s.Get(ctx, sess.ID); err != nil || one.ExpertID != "financial-modeler" {
		t.Fatalf("Get: %+v %v", one, err)
	}

	if err := s.SetExpert(ctx, sess.ID, "data-analyst"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Expert(ctx, sess.ID); got != "data-analyst" {
		t.Fatalf("a new binding replaces the old one: %q", got)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM chat_session_experts`); n != 1 {
		t.Fatalf("%d rows for one conversation", n)
	}

	if err := s.SetExpert(ctx, sess.ID, "  "); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Expert(ctx, sess.ID); got != "" {
		t.Fatalf("an empty id unbinds: %q", got)
	}
	if row, _ := sessionRow(t, s, sess.ID); row.ExpertID != "" {
		t.Fatalf("List after unbinding: %+v", row)
	}
	if err := s.SetExpert(ctx, " ", "data-analyst"); err == nil {
		t.Fatal("binding no conversation must fail")
	}
}

// A binding can be made before the conversation row exists (ACP and gateway
// sessions are created lazily); it must not be refused or lost.
func TestExpertBindingDoesNotNeedAConversationRow(t *testing.T) {
	s, _ := deletionStore(t)
	ctx := context.Background()
	if err := s.SetExpert(ctx, "acp-1", "code-reviewer"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Expert(ctx, "acp-1"); got != "code-reviewer" {
		t.Fatalf("expert = %q", got)
	}
}

func TestForkOfAConversationWithoutAnExpertHasNone(t *testing.T) {
	s, _ := deletionStore(t)
	ctx := context.Background()
	if err := s.EnsureSession(ctx, "plain"); err != nil {
		t.Fatal(err)
	}
	res, err := s.ForkSession(ctx, "plain", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Session.ExpertID != "" {
		t.Fatalf("expert on the copy: %q", res.Session.ExpertID)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM chat_session_experts`); n != 0 {
		t.Fatalf("%d expert rows appeared", n)
	}
}
