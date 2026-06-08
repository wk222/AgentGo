package agent

import (
	"context"
	"testing"
)

func TestRunnerWithSessionModePreservesExplicitContextMode(t *testing.T) {
	r := &Runner{}
	r.SetSessionMode(SessionMode{Profile: ModeAssistant, Canvas: CanvasFocused})
	r.SetSessionModeForSession("s1", SessionMode{Profile: ModeAdmin, Canvas: CanvasDeep})

	ctx := WithSessionMode(context.Background(), SessionMode{Profile: ModeAppMatrix, Canvas: CanvasDeep})
	got := SessionModeFromContext(r.withSessionMode(ctx, "s1"))

	if got.Profile != ModeAppMatrix || got.Canvas != CanvasDeep {
		t.Fatalf("mode=%+v, want app_matrix/deep", got)
	}
}

func TestRunnerWithSessionModeUsesSessionOverride(t *testing.T) {
	r := &Runner{}
	r.SetSessionMode(SessionMode{Profile: ModeAssistant, Canvas: CanvasFocused})
	r.SetSessionModeForSession("s1", SessionMode{Profile: ModeAdmin, Canvas: CanvasDeep})

	got := SessionModeFromContext(r.withSessionMode(context.Background(), "s1"))

	if got.Profile != ModeAdmin || got.Canvas != CanvasDeep {
		t.Fatalf("mode=%+v, want admin/deep", got)
	}
}

func TestRunnerWithSessionModeFallsBackToDefault(t *testing.T) {
	r := &Runner{}
	r.SetSessionMode(SessionMode{Profile: ModeAssistant, Canvas: CanvasFocused})

	got := SessionModeFromContext(r.withSessionMode(context.Background(), "unknown"))

	if got.Profile != ModeAssistant || got.Canvas != CanvasFocused {
		t.Fatalf("mode=%+v, want assistant/focused", got)
	}
}
