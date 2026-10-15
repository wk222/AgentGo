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

func TestRunnerPersistsSessionModesAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	r1 := &Runner{
		dataDir:      dir,
		sessionModes: make(map[string]SessionMode),
	}
	r1.SetSessionMode(SessionMode{Profile: ModeAssistant, Canvas: CanvasFocused})
	r1.SetSessionModeForSession("sess-creative", SessionMode{Profile: ModeAppMatrix, Canvas: CanvasBalanced})

	r2 := &Runner{
		dataDir:      dir,
		sessionModes: make(map[string]SessionMode),
	}
	r2.loadPersistedSessionModes()

	if got := r2.SessionMode(); got.Profile != ModeAssistant || got.Canvas != CanvasFocused {
		t.Fatalf("reloaded default mode=%+v, want assistant/focused", got)
	}
	if got := r2.SessionModeForSession("sess-creative"); got.Profile != ModeAppMatrix || got.Canvas != CanvasBalanced {
		t.Fatalf("reloaded session mode=%+v, want app_matrix/balanced", got)
	}
}
