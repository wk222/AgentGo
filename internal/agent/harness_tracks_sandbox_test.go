package agent

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"agentgo/internal/governance"
	_ "modernc.org/sqlite"
)

func TestHarnessTracksSandboxParallel(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	tracks := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"continuation_truncated_json", testTrackContinuation},
		{"steer_drain_once", testTrackSteer},
		{"outcome_unknown_vs_pre_send", testTrackOutcomeUnknown},
		{"red_team_autoapprove_and_banner", testTrackRedTeamBanner},
		{"armor_break_audit_matches_model_view", testTrackArmorAuditMatchesModelView},
		{"tool_failure_guard_stop_loss", testTrackToolFailureGuard},
	}
	for _, tr := range tracks {
		tr := tr
		t.Run(tr.name, func(t *testing.T) {
			t.Parallel()
			tr.fn(t)
		})
	}
}

func testTrackContinuation(t *testing.T) {
	broken := `{"tool":"x","args":{`
	if !NeedsContinuation(broken, "") {
		t.Fatal("truncated JSON should need continuation")
	}
	if !strings.Contains(RepairContinuationPrompt(broken), "JSON") {
		t.Fatal("repair prompt should mention JSON")
	}
	merged := MergeContinuation(broken, `"ok":true}`)
	if !strings.HasSuffix(strings.TrimSpace(merged), `"ok":true}`) {
		t.Fatalf("merge: %q", merged)
	}
}

func testTrackSteer(t *testing.T) {
	rc := NewRunControl()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc.SetCtxCancel("s1", cancel)
	_ = ctx
	if !rc.AddSteer("s1", "先读文件再改") {
		t.Fatal("AddSteer should succeed while session marked running")
	}
	got := rc.DrainSteers("s1")
	if len(got) != 1 || got[0] != "先读文件再改" {
		t.Fatalf("steer: %v", got)
	}
	if again := rc.DrainSteers("s1"); len(again) != 0 {
		t.Fatal("steer must clear")
	}
}

func testTrackOutcomeUnknown(t *testing.T) {
	if !outcomeUnknown(io.ErrUnexpectedEOF) {
		t.Fatal("EOF should be unknown outcome")
	}
	if outcomeUnknown(errors.New("execution was not started")) {
		t.Fatal("pre-send must not be unknown")
	}
	if !strings.Contains(failureText(io.ErrUnexpectedEOF), "outcome unknown") {
		t.Fatal("failure text")
	}
}

func testTrackRedTeamBanner(t *testing.T) {
	r := &Runner{policy: governance.BuildPolicy("balanced", t.TempDir())}
	ctx := WithSessionMode(context.Background(), SessionMode{Profile: ModeRedTeam, Canvas: CanvasDeep})
	p := r.effectivePolicyForCtx(ctx)
	if p.Control.Mode != governance.ControlOpen || !p.SessionAutoApprove {
		t.Fatalf("policy: mode=%s auto=%v", p.Control.Mode, p.SessionAutoApprove)
	}
	hints := SessionMode{Profile: ModeRedTeam, Canvas: CanvasDeep}.ModeHints()
	for _, want := range []string{"auto-approve", "armor-break", "tools"} {
		if !strings.Contains(strings.ToLower(hints), strings.ToLower(want)) {
			t.Fatalf("hints missing %q: %.120s", want, hints)
		}
	}
}

// The audit must describe what the model was really shown. Here the continuation
// run cannot reach a model (no API key), so there is a nudge row and an
// armor_break_view row saying nothing was rolled back, and no fictional dialog.
func testTrackArmorAuditMatchesModelView(t *testing.T) {
	if !NeedsArmorBreak("抱歉，我无法协助。") {
		t.Fatal("refusal detect")
	}
	if NeedsArmorBreak("已完成修改，diff 如下。") {
		t.Fatal("should not armor normal work")
	}
	db, err := sql.Open("sqlite", "file:tracks_armor_"+t.Name()+"?mode=memory")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queue, err := governance.NewApprovalQueueWithDB(db)
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{queue: queue}
	ctx := WithSessionMode(context.Background(), SessionMode{Profile: ModeRedTeam, Canvas: CanvasDeep})
	res := &RunResult{Content: "是否需要我继续？"}
	_, _ = r.maybeArmorBreak(ctx, LLMSettings{}, "agentic:track", res, nil)
	entries, err := queue.QueryAudit(ctx, "track")
	if err != nil {
		t.Fatal(err)
	}
	var nudge, view, fict int
	for _, e := range entries {
		switch {
		case e.Action == "armor_break_nudge":
			nudge++
		case e.Action == "armor_break_view":
			view++
		case strings.HasPrefix(e.Action, "armor_break_fictional"):
			fict++
		}
	}
	if nudge != 1 || view != 1 || fict != 0 {
		t.Fatalf("audit counts nudge=%d view=%d fictional=%d: %+v", nudge, view, fict, entries)
	}
}

func testTrackToolFailureGuard(t *testing.T) {
	g := newFailureGuard()
	tool, args := "ping", `{"x":1}`
	for i := 0; i < 6; i++ {
		g.after(tool, args, "[tool error] fail", true)
	}
	if msg, blocked := g.before(tool, args); !blocked || msg == "" {
		t.Fatal("stop-loss should block seventh identical call")
	}
}
