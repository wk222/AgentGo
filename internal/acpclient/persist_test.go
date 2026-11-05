package acpclient

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// rig is a manager with a session store whose agents can load sessions. Each agent it launches
// hands out the ids "<tag>-1", "<tag>-2", and so on, so a test can tell the sessions apart.
type rig struct {
	m      *Manager
	mu     sync.Mutex
	agents []*fakeAgent
}

func newRig(t *testing.T, path, tag string, tweak func(*fakeAgent)) *rig {
	t.Helper()
	r := &rig{m: NewManager()}
	r.m.SetStore(path)
	r.m.launch = func(ctx context.Context, cfg Config) (*Conn, error) {
		a := newFake(t)
		a.loadSession = true
		r.mu.Lock()
		r.agents = append(r.agents, a)
		a.sessionID = fmt.Sprintf("%s-%d", tag, len(r.agents))
		r.mu.Unlock()
		if tweak != nil {
			tweak(a)
		}
		return connect(ctx, cfg, a.transport())
	}
	t.Cleanup(r.m.CloseAll)
	return r
}

func (r *rig) agent(i int) *fakeAgent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.agents[i]
}

func (r *rig) run(t *testing.T, label string, cfg Config, prompt string) *Turn {
	t.Helper()
	turn, err := r.m.Run(context.Background(), label, "fake", cfg, prompt, nil)
	if err != nil {
		t.Fatalf("run %q: %v", label, err)
	}
	return turn
}

func storePath(t *testing.T) string { return filepath.Join(t.TempDir(), "external_agents.json") }

var workCfg = Config{Name: "fake", Cwd: "/w"}

func TestSavedSessionSurvivesARestart(t *testing.T) {
	path := storePath(t)
	first := newRig(t, path, "one", nil)
	if turn := first.run(t, "review", workCfg, "first"); turn.Resumed || turn.SessionID != "one-1" {
		t.Fatalf("a first run starts a session: %+v", turn)
	}
	first.m.CloseAll() // the app exits

	second := newRig(t, path, "two", nil)
	if got := second.m.List(); len(got) != 1 || got[0].Label != "review" || got[0].Live || got[0].Agent != "fake" || got[0].Cwd != "/w" {
		t.Fatalf("a session the agent can load must be listed as carried over: %+v", got)
	}
	turn := second.run(t, "review", workCfg, "second")
	if !turn.Resumed || turn.SessionID != "one-1" {
		t.Fatalf("the same label must carry on the earlier session: %+v", turn)
	}
	if loaded, created := asked(second.agent(0)); len(loaded) != 1 || loaded[0] != "one-1" || created != 0 {
		t.Fatalf("loaded=%v created=%d", loaded, created)
	}
	if got := second.m.List(); len(got) != 1 || !got[0].Live {
		t.Fatalf("now it is live again: %+v", got)
	}
}

func TestIdleCloseKeepsTheSavedSession(t *testing.T) {
	r := newRig(t, storePath(t), "s", nil)
	r.m.idle = 150 * time.Millisecond
	r.run(t, "review", workCfg, "q")
	select {
	case <-r.agent(0).exited:
	case <-time.After(3 * time.Second):
		t.Fatal("an idle session must be closed")
	}
	if got := r.m.List(); len(got) != 1 || got[0].Live {
		t.Fatalf("an idle session stays carried over: %+v", got)
	}
	if turn := r.run(t, "review", workCfg, "again"); !turn.Resumed || turn.SessionID != "s-1" {
		t.Fatalf("%+v", turn)
	}
	if len(r.agents) != 2 {
		t.Fatalf("a second agent must have been started: %d", len(r.agents))
	}
}

func TestEvictionKeepsTheSavedSession(t *testing.T) {
	r := newRig(t, storePath(t), "s", nil)
	r.m.maxLive = 1
	r.run(t, "a", workCfg, "q")
	time.Sleep(5 * time.Millisecond)
	r.run(t, "b", workCfg, "q") // a is evicted to make room
	got := r.m.List()
	if len(got) != 2 || got[0].Label != "a" || got[0].Live || got[1].Label != "b" || !got[1].Live {
		t.Fatalf("an evicted session stays carried over: %+v", got)
	}
	if turn := r.run(t, "a", workCfg, "back"); !turn.Resumed || turn.SessionID != "s-1" {
		t.Fatalf("%+v", turn)
	}
}

func TestCloseForgetsTheSavedSession(t *testing.T) {
	path := storePath(t)
	first := newRig(t, path, "one", nil)
	first.run(t, "review", workCfg, "q")
	if !first.m.Close("review") || first.m.Close("review") {
		t.Fatal("Close must report whether there was anything to end")
	}
	if got := first.m.List(); len(got) != 0 {
		t.Fatalf("a closed session is gone for good: %+v", got)
	}
	second := newRig(t, path, "two", nil)
	turn := second.run(t, "review", workCfg, "fresh start")
	if turn.Resumed || turn.ResumeNote != "" || turn.SessionID != "two-1" {
		t.Fatalf("nothing was asked to be carried on: %+v", turn)
	}
	if loaded, created := asked(second.agent(0)); len(loaded) != 0 || created != 1 {
		t.Fatalf("loaded=%v created=%d", loaded, created)
	}
}

func TestClosingACarriedOverSessionForgetsIt(t *testing.T) {
	path := storePath(t)
	first := newRig(t, path, "one", nil)
	first.run(t, "review", workCfg, "q")
	first.m.CloseAll()

	second := newRig(t, path, "two", nil)
	if !second.m.Close("review") {
		t.Fatal("a saved session counts as something to end")
	}
	if second.m.Close("review") || len(second.m.List()) != 0 {
		t.Fatalf("it must be gone: %+v", second.m.List())
	}
}

func TestSavedSessionOfAnotherJobIsNotResumed(t *testing.T) {
	read := Config{Name: "fake", Cwd: "/w", Permission: ReadOnly}
	cases := []struct {
		name   string
		first  Config
		second Config
		agent  string
		resume bool
	}{
		{"same job", read, read, "fake", true},
		{"other directory", read, Config{Name: "fake", Cwd: "/elsewhere"}, "fake", false},
		{"other agent", read, read, "another", false},
		{"more freedom now", read, Config{Name: "fake", Cwd: "/w", Permission: Full}, "fake", true},
		{"less freedom now", Config{Name: "fake", Cwd: "/w", Permission: Edit}, read, "fake", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := storePath(t)
			first := newRig(t, path, "one", nil)
			if _, err := first.m.Run(context.Background(), "job", "fake", c.first, "q", nil); err != nil {
				t.Fatal(err)
			}
			first.m.CloseAll()

			second := newRig(t, path, "two", nil)
			turn, err := second.m.Run(context.Background(), "job", c.agent, c.second, "q", nil)
			if err != nil {
				t.Fatal(err)
			}
			loaded, created := asked(second.agent(0))
			if c.resume && (!turn.Resumed || len(loaded) != 1 || created != 0) {
				t.Fatalf("it must carry on: %+v loaded=%v created=%d", turn, loaded, created)
			}
			if !c.resume && (turn.Resumed || len(loaded) != 0 || created != 1) {
				t.Fatalf("it must start over: %+v loaded=%v created=%d", turn, loaded, created)
			}
			if got := second.m.List(); len(got) != 1 || got[0].Agent != c.agent || got[0].Cwd != c.second.Cwd {
				t.Fatalf("the record follows the job that ran last: %+v", got)
			}
		})
	}
}

func TestAgentThatCannotLoadIsNotSaved(t *testing.T) {
	path := storePath(t)
	r := newRig(t, path, "s", func(a *fakeAgent) { a.loadSession = false })
	r.run(t, "review", workCfg, "q")
	r.m.CloseAll()
	if got := newRig(t, path, "t", nil).m.List(); len(got) != 0 {
		t.Fatalf("a record for an agent that cannot load would promise what cannot happen: %+v", got)
	}
}

func TestRecordFollowsTheNewSessionWhenTheOldOneIsGone(t *testing.T) {
	path := storePath(t)
	first := newRig(t, path, "one", nil)
	first.run(t, "review", workCfg, "q")
	first.m.CloseAll()

	second := newRig(t, path, "two", func(a *fakeAgent) { a.loadErr = -32002 })
	turn := second.run(t, "review", workCfg, "q")
	if turn.Resumed || turn.SessionID != "two-1" || turn.ResumeNote == "" {
		t.Fatalf("the agent has forgotten the session; say so and start over: %+v", turn)
	}
	rec, ok := second.m.savedStore().get("review")
	if !ok || rec.SessionID != "two-1" {
		t.Fatalf("the label must now point at the new session: %+v %v", rec, ok)
	}
}

func TestNoStoreMeansNothingSurvives(t *testing.T) {
	first := newRig(t, "", "one", nil) // "" turns the store off
	first.run(t, "review", workCfg, "q")
	first.m.CloseAll()
	if got := first.m.List(); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	if turn := first.run(t, "review", workCfg, "again"); turn.Resumed {
		t.Fatalf("%+v", turn)
	}
}

func TestOverlongLabelIsRefused(t *testing.T) {
	r := newRig(t, storePath(t), "s", nil)
	label := strings.Repeat("x", maxLabelLen+1)
	if _, err := r.m.Run(context.Background(), label, "fake", workCfg, "q", nil); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("a label the store would refuse must be refused up front: %v", err)
	}
	if len(r.agents) != 0 {
		t.Fatal("no agent should have been started for it")
	}
}

func TestSamePath(t *testing.T) {
	if !samePath("/w/", "/w") || samePath("/a", "/b") {
		t.Fatal("paths are compared after cleaning")
	}
	if got := samePath(`C:\Work`, `c:\work`); got != (os.PathSeparator == '\\') {
		t.Fatalf("case must matter exactly where the file system ignores it: %v", got)
	}
}
