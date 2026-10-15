package agent

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	agentdb "agentgo/internal/db"
	"agentgo/internal/sessions"

	_ "modernc.org/sqlite"
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

func openModeTestStore(t *testing.T) *sessions.Store {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := agentdb.Configure(conn); err != nil {
		t.Fatal(err)
	}
	st, err := sessions.Open(conn)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRunnerPersistsSessionModesAcrossRestart(t *testing.T) {
	st := openModeTestStore(t)
	r1 := &Runner{}
	r1.SetSessionsStore(st)
	r1.SetSessionMode(SessionMode{Profile: ModeAssistant, Canvas: CanvasFocused})
	r1.SetSessionModeForSession("sess-creative", SessionMode{Profile: ModeAppMatrix, Canvas: CanvasBalanced})
	r1.SetSessionModeForSession("sess-gone", SessionMode{Profile: ModeAdmin, Canvas: CanvasDeep})
	r1.ClearSessionModeForSession("sess-gone")

	r2 := &Runner{}
	r2.SetSessionsStore(st)

	if got := r2.SessionMode(); got.Profile != ModeAssistant || got.Canvas != CanvasFocused {
		t.Fatalf("reloaded default mode=%+v, want assistant/focused", got)
	}
	if got := r2.SessionModeForSession("sess-creative"); got.Profile != ModeAppMatrix || got.Canvas != CanvasBalanced {
		t.Fatalf("reloaded session mode=%+v, want app_matrix/balanced", got)
	}
	if got := r2.SessionModeForSession("sess-gone"); got.Profile != ModeAssistant || got.Canvas != CanvasFocused {
		t.Fatalf("cleared session should fall back to default, got %+v", got)
	}
}

func TestRunnerImportsLegacySessionModesFile(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"default":{"Profile":"admin","Canvas":"deep"},"sessions":{"s1":{"Profile":"app_matrix","Canvas":"balanced"}}}`
	path := filepath.Join(dir, "session_modes.json")
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &Runner{dataDir: dir}
	r.SetSessionsStore(openModeTestStore(t))

	if got := r.SessionMode(); got.Profile != ModeAdmin || got.Canvas != CanvasDeep {
		t.Fatalf("default mode=%+v, want admin/deep", got)
	}
	if got := r.SessionModeForSession("s1"); got.Profile != ModeAppMatrix {
		t.Fatalf("s1 mode=%+v, want app_matrix", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy file should be removed after import, stat err=%v", err)
	}
}
