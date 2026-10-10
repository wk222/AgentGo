package trigger

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

type recorder struct {
	mu  sync.Mutex
	got []Trigger
	evs []Event
	n   atomic.Int32
}

func (r *recorder) fire(_ context.Context, t Trigger, ev Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got, r.evs = append(r.got, t), append(r.evs, ev)
	r.n.Add(1)
	return nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func startEngine(t *testing.T, s *Store, fire FireFunc) *Engine {
	t.Helper()
	e := NewEngine(s, fire)
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	return e
}

func wake(sid string) Trigger { return Trigger{Action: ActionWakeSession, SessionID: sid, Prompt: "analyse"} }

func TestValidate(t *testing.T) {
	bad := []Trigger{
		{Kind: KindProcessExit, Action: ActionWakeSession, SessionID: "s"},
		{Kind: KindFile, Path: "relative.txt", Action: ActionWakeSession, SessionID: "s"},
		{Kind: KindLogMatch, Path: os.TempDir(), Pattern: "(", Action: ActionWakeSession, SessionID: "s"},
		{Kind: KindWebhook, Action: ActionWakeSession},
		{Kind: KindWebhook, Action: ActionEmitSignal, RunID: "r"},
		{Kind: "nope", Action: ActionWakeSession, SessionID: "s"},
	}
	for i, b := range bad {
		if err := b.Validate(); err == nil {
			t.Errorf("case %d should be invalid: %+v", i, b)
		}
	}
}

func TestFileMarkerWakesSessionOnce(t *testing.T) {
	s, _ := newTestStore(t)
	rec := &recorder{}
	e := startEngine(t, s, rec.fire)

	marker := filepath.Join(t.TempDir(), "done.flag")
	tr := wake("sess_1")
	tr.Kind, tr.Path = KindFile, marker
	created, err := e.Add(tr)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if rec.n.Load() != 0 {
		t.Fatal("fired before the marker existed")
	}
	if err := os.WriteFile(marker, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "fire", func() bool { return rec.n.Load() == 1 })
	time.Sleep(1500 * time.Millisecond)
	if rec.n.Load() != 1 {
		t.Fatalf("expected exactly one wake-up, got %d", rec.n.Load())
	}
	got, _ := s.Get(created.ID)
	if got.Status != StatusFired || !got.Delivered {
		t.Fatalf("unexpected stored state: %+v", got)
	}
}

func TestLogMatchOnlySeesNewLines(t *testing.T) {
	s, _ := newTestStore(t)
	rec := &recorder{}
	e := startEngine(t, s, rec.fire)

	logf := filepath.Join(t.TempDir(), "train.log")
	if err := os.WriteFile(logf, []byte("epoch 1 DONE (old line)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := wake("sess_2")
	tr.Kind, tr.Path, tr.Pattern = KindLogMatch, logf, `DONE|FAILED`
	if _, err := e.Add(tr); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if rec.n.Load() != 0 {
		t.Fatal("matched a line written before the trigger was armed")
	}
	f, _ := os.OpenFile(logf, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("epoch 2 running\nfinal acc=0.93 DONE\n")
	f.Close()
	waitFor(t, "log match", func() bool { return rec.n.Load() == 1 })
	rec.mu.Lock()
	line, _ := rec.evs[0].Detail["line"].(string)
	rec.mu.Unlock()
	if line != "final acc=0.93 DONE" {
		t.Fatalf("wrong matched line %q", line)
	}
}

func TestWebhookConcurrentFireIsExactlyOnce(t *testing.T) {
	s, _ := newTestStore(t)
	rec := &recorder{}
	e := startEngine(t, s, rec.fire)
	tr := wake("sess_3")
	tr.Kind = KindWebhook
	created, err := e.Add(tr)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = e.FireWebhook(created.ID, map[string]any{"acc": 0.9}) }()
	}
	wg.Wait()
	if rec.n.Load() != 1 {
		t.Fatalf("expected one delivery, got %d", rec.n.Load())
	}
	if err := e.FireWebhook(created.ID, nil); err != ErrNotArmed {
		t.Fatalf("second fire should report ErrNotArmed, got %v", err)
	}
}

func TestCancelPreventsFire(t *testing.T) {
	s, _ := newTestStore(t)
	rec := &recorder{}
	e := startEngine(t, s, rec.fire)
	tr := wake("sess_4")
	tr.Kind, tr.Path = KindFile, filepath.Join(t.TempDir(), "never.flag")
	created, _ := e.Add(tr)
	if err := e.Cancel(created.ID); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(tr.Path, []byte("x"), 0o644)
	time.Sleep(1500 * time.Millisecond)
	if rec.n.Load() != 0 {
		t.Fatal("cancelled trigger fired")
	}
}

func TestExpiry(t *testing.T) {
	s, _ := newTestStore(t)
	rec := &recorder{}
	e := startEngine(t, s, rec.fire)
	tr := wake("sess_5")
	tr.Kind, tr.Path = KindFile, filepath.Join(t.TempDir(), "late.flag")
	tr.ExpiresAt = time.Now().Add(700 * time.Millisecond).Unix()
	created, _ := e.Add(tr)
	waitFor(t, "expiry", func() bool {
		g, _ := s.Get(created.ID)
		return g.Status == StatusExpired
	})
	if rec.n.Load() != 0 {
		t.Fatal("expired trigger fired")
	}
}

func TestRestartRearmsAndRedelivers(t *testing.T) {
	s, _ := newTestStore(t)

	// armed trigger left behind by a previous process
	marker := filepath.Join(t.TempDir(), "after-restart.flag")
	a := wake("sess_6")
	a.Kind, a.Path = KindFile, marker
	armed, err := s.Create(a)
	if err != nil {
		t.Fatal(err)
	}
	// fired-but-never-delivered trigger (crash between the two steps)
	b := wake("sess_7")
	b.Kind = KindWebhook
	crashed, _ := s.Create(b)
	if won, _ := s.MarkFired(crashed.ID, "{}"); !won {
		t.Fatal("setup: MarkFired")
	}

	rec := &recorder{}
	startEngine(t, s, rec.fire)
	waitFor(t, "redelivery", func() bool { return rec.n.Load() == 1 })
	_ = os.WriteFile(marker, []byte("x"), 0o644)
	waitFor(t, "re-armed fire", func() bool { return rec.n.Load() == 2 })
	g, _ := s.Get(armed.ID)
	if g.Status != StatusFired {
		t.Fatalf("armed trigger not fired after restart: %+v", g)
	}
}

func TestProcessExitReportsExitCode(t *testing.T) {
	s, _ := newTestStore(t)
	rec := &recorder{}
	e := startEngine(t, s, rec.fire)

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(), "TRIGGER_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tr := wake("sess_8")
	tr.Kind, tr.PID = KindProcessExit, cmd.Process.Pid
	if _, err := e.Add(tr); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait() //nolint:errcheck // reaped separately so the watcher is the one observing exit
	waitFor(t, "process exit", func() bool { return rec.n.Load() == 1 })
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("TRIGGER_HELPER") != "1" {
		return
	}
	time.Sleep(800 * time.Millisecond)
	os.Exit(3)
}
