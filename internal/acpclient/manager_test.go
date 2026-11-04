package acpclient

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeManager launches in-process fakes and counts them.
func fakeManager(t *testing.T) (*Manager, *atomic.Int32, *[]*fakeAgent) {
	t.Helper()
	m := NewManager()
	var launched atomic.Int32
	var agents []*fakeAgent
	m.launch = func(ctx context.Context, cfg Config) (*Conn, error) {
		launched.Add(1)
		a := newFake(t)
		agents = append(agents, a)
		return connect(ctx, cfg, a.transport())
	}
	t.Cleanup(m.CloseAll)
	return m, &launched, &agents
}

func TestUnnamedRunIsOneShot(t *testing.T) {
	m, launched, agents := fakeManager(t)
	cfg := Config{Name: "fake", Cwd: "/w"}
	for i := 0; i < 2; i++ {
		turn, err := m.Run(context.Background(), "", "fake", cfg, "q", nil)
		if err != nil || turn.Text != "ok" {
			t.Fatalf("run %d: %+v %v", i, turn, err)
		}
	}
	if launched.Load() != 2 || len(m.List()) != 0 {
		t.Fatalf("launched=%d live=%v", launched.Load(), m.List())
	}
	for _, a := range *agents {
		select {
		case <-a.exited:
		case <-time.After(3 * time.Second):
			t.Fatal("a one-shot agent must be stopped when its turn is done")
		}
	}
}

func TestNamedSessionIsReused(t *testing.T) {
	m, launched, agents := fakeManager(t)
	cfg := Config{Name: "fake", Cwd: "/w"}
	for _, q := range []string{"first", "second"} {
		if _, err := m.Run(context.Background(), "review", "fake", cfg, q, nil); err != nil {
			t.Fatal(err)
		}
	}
	if launched.Load() != 1 {
		t.Fatalf("a named session must be launched once, not %d times", launched.Load())
	}
	if got := (*agents)[0].prompts; len(got) != 2 || got[1] != "second" {
		t.Fatalf("the same agent must see both prompts: %v", got)
	}
	live := m.List()
	if len(live) != 1 || live[0].Label != "review" || live[0].Permission != "read_only" {
		t.Fatalf("live: %+v", live)
	}
	if !m.Close("review") || m.Close("review") {
		t.Fatal("Close must report whether there was a session")
	}
}

func TestLabelCannotBeReusedForADifferentJob(t *testing.T) {
	m, _, _ := fakeManager(t)
	if _, err := m.Run(context.Background(), "x", "fake", Config{Name: "fake", Cwd: "/a"}, "q", nil); err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]Config{
		"other cwd":        {Name: "fake", Cwd: "/b"},
		"higher permision": {Name: "fake", Cwd: "/a", Permission: Full},
	} {
		if _, err := m.Run(context.Background(), "x", "fake", cfg, "q", nil); err == nil || !strings.Contains(err.Error(), "already open") {
			t.Errorf("%s: a session opened read-only must not silently turn into something else: %v", name, err)
		}
	}
	if _, err := m.Run(context.Background(), "x", "other-agent", Config{Name: "fake", Cwd: "/a"}, "q", nil); err == nil {
		t.Error("a different agent under the same label must be refused")
	}
}

func TestDeadSessionIsReplaced(t *testing.T) {
	m, launched, agents := fakeManager(t)
	cfg := Config{Name: "fake", Cwd: "/w"}
	if _, err := m.Run(context.Background(), "s", "fake", cfg, "q", nil); err != nil {
		t.Fatal(err)
	}
	(*agents)[0].out.(interface{ Close() error }).Close() // it crashes between turns
	time.Sleep(100 * time.Millisecond)
	if len(m.List()) != 0 {
		t.Fatal("a dead session must not be listed")
	}
	if turn, err := m.Run(context.Background(), "s", "fake", cfg, "again", nil); err != nil || turn.Text != "ok" {
		t.Fatalf("%+v %v", turn, err)
	}
	if launched.Load() != 2 {
		t.Fatalf("launched=%d, want a fresh agent", launched.Load())
	}
}

func TestLiveSessionsAreCapped(t *testing.T) {
	m, _, agents := fakeManager(t)
	m.maxLive = 2
	cfg := Config{Name: "fake", Cwd: "/w"}
	for _, label := range []string{"a", "b", "c"} {
		if _, err := m.Run(context.Background(), label, "fake", cfg, "q", nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.List(); len(got) != 2 || got[0].Label != "b" || got[1].Label != "c" {
		t.Fatalf("the oldest must be evicted: %+v", got)
	}
	select {
	case <-(*agents)[0].exited:
	case <-time.After(3 * time.Second):
		t.Fatal("an evicted agent must be stopped, not leaked")
	}
}

func TestIdleSessionsAreClosed(t *testing.T) {
	m, _, agents := fakeManager(t)
	m.idle = 150 * time.Millisecond
	if _, err := m.Run(context.Background(), "s", "fake", Config{Name: "fake", Cwd: "/w"}, "q", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-(*agents)[0].exited:
	case <-time.After(3 * time.Second):
		t.Fatal("an idle session must be closed")
	}
	if len(m.List()) != 0 {
		t.Fatalf("live: %+v", m.List())
	}
}

func TestCloseAllStopsEverything(t *testing.T) {
	m, _, agents := fakeManager(t)
	cfg := Config{Name: "fake", Cwd: "/w"}
	for _, l := range []string{"a", "b"} {
		_, _ = m.Run(context.Background(), l, "fake", cfg, "q", nil)
	}
	m.CloseAll()
	for _, a := range *agents {
		select {
		case <-a.exited:
		case <-time.After(3 * time.Second):
			t.Fatal("CloseAll left an agent running")
		}
	}
}

func TestRegistry(t *testing.T) {
	if p, err := Lookup("Claude", nil); err != nil || !strings.Contains(p.Command, "claude-agent-acp") || p.Binary != "npx" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := Lookup("nope", nil); err == nil || !strings.Contains(err.Error(), "gemini") {
		t.Fatalf("an unknown agent must list the known ones: %v", err)
	}
	custom, err := ParseCustom(`{"Mine":"my-agent --acp", "gemini":"gemini --acp --yolo"}`)
	if err != nil {
		t.Fatal(err)
	}
	mine, err := Lookup("mine", custom)
	if err != nil || mine.Command != "my-agent --acp" || mine.Binary != "my-agent" {
		t.Fatalf("%+v %v", mine, err)
	}
	if g, _ := Lookup("gemini", custom); g.Command != "gemini --acp --yolo" {
		t.Fatalf("a configured agent must override a preset: %+v", g)
	}
	if _, err := ParseCustom(`{not json`); err == nil {
		t.Fatal("a typo in the setting must not be silent")
	}
	if m, err := ParseCustom("  "); err != nil || m != nil {
		t.Fatalf("empty setting: %v %v", m, err)
	}
}
