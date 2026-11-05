package acpclient

import (
	"context"
	"strings"
	"testing"
	"time"
)

func resumeCfg(id string) Config { return Config{Name: "fake", Cwd: "/work", ResumeSessionID: id} }

// openFake connects to a fake agent the way Launch would.
func openFake(t *testing.T, a *fakeAgent, cfg Config) (*Conn, error) {
	t.Helper()
	c, err := connect(context.Background(), cfg, a.transport())
	if err == nil {
		t.Cleanup(c.Close)
	}
	return c, err
}

// asked returns what the agent was asked to load and how many sessions it was asked to create.
func asked(a *fakeAgent) (loaded []string, created int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.loaded...), a.newSessions
}

func TestResumeCarriesOnAndIgnoresTheReplay(t *testing.T) {
	a := newFake(t)
	a.loadSession, a.sessionID = true, "old"
	a.replay = []string{"an earlier question", "an earlier answer"} // the agent re-sends its history while loading
	c, err := openFake(t, a, resumeCfg("old"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Resumed() || c.SessionID() != "old" || c.ResumeNote() != "" || !c.CanResume() {
		t.Fatalf("resumed=%v id=%q note=%q", c.Resumed(), c.SessionID(), c.ResumeNote())
	}
	if loaded, created := asked(a); len(loaded) != 1 || loaded[0] != "old" || created != 0 {
		t.Fatalf("loaded=%v created=%d: carrying on must not also start a new session", loaded, created)
	}

	turn, err := c.Prompt(context.Background(), "and now?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Text != "ok" {
		t.Fatalf("the replayed history leaked into the answer: %q", turn.Text)
	}
	if !turn.Resumed || turn.SessionID != "old" || turn.ResumeNote != "" {
		t.Fatalf("the first turn must say the session was carried on: %+v", turn)
	}
	again, err := c.Prompt(context.Background(), "once more", nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.Resumed || again.ResumeNote != "" || again.SessionID != "old" {
		t.Fatalf("how the session began is reported once, the id every time: %+v", again)
	}
}

func TestResumeFallsBackWhenTheAgentCannotLoad(t *testing.T) {
	a := newFake(t) // loadSession is off
	c, err := openFake(t, a, resumeCfg("old"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Resumed() || c.CanResume() || c.SessionID() != "s1" || !strings.Contains(c.ResumeNote(), "cannot load") {
		t.Fatalf("resumed=%v can=%v id=%q note=%q", c.Resumed(), c.CanResume(), c.SessionID(), c.ResumeNote())
	}
	if loaded, created := asked(a); len(loaded) != 0 || created != 1 {
		t.Fatalf("an agent that cannot load must not be asked to: loaded=%v created=%d", loaded, created)
	}
	turn, err := c.Prompt(context.Background(), "q", nil)
	if err != nil || turn.Resumed || !strings.Contains(turn.ResumeNote, "cannot load") {
		t.Fatalf("the turn must say why the earlier session is gone: %+v %v", turn, err)
	}
}

func TestResumeFallsBackWhenTheSessionIsGone(t *testing.T) {
	a := newFake(t)
	a.loadSession, a.loadErr, a.sessionID = true, -32002, "fresh"
	c, err := openFake(t, a, resumeCfg("old"))
	if err != nil {
		t.Fatalf("an agent that has forgotten the session is not a failure: %v", err)
	}
	if c.Resumed() || c.SessionID() != "fresh" || !c.CanResume() {
		t.Fatalf("resumed=%v id=%q", c.Resumed(), c.SessionID())
	}
	if note := c.ResumeNote(); !strings.Contains(note, "old") || !strings.Contains(note, "not found") {
		t.Fatalf("the note must carry the agent's own reason: %q", note)
	}
	if loaded, created := asked(a); len(loaded) != 1 || created != 1 {
		t.Fatalf("loaded=%v created=%d", loaded, created)
	}
}

func TestResumeSignsInWhenTheAgentAsks(t *testing.T) {
	a := newFake(t)
	a.loadSession, a.authRequired, a.sessionID = true, true, "old"
	c, err := openFake(t, a, resumeCfg("old"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Resumed() {
		t.Fatalf("sign-in during session/load must lead to the resume, note=%q", c.ResumeNote())
	}
	a.mu.Lock()
	authed := a.authed
	a.mu.Unlock()
	if loaded, created := asked(a); !authed || len(loaded) != 2 || created != 0 {
		t.Fatalf("authed=%v loaded=%v created=%d", authed, loaded, created)
	}
}

// An agent that does not answer session/load is not the same as one that says no: starting a new
// session would hide that something is wrong with the agent.
func TestResumeThatHangsIsAnError(t *testing.T) {
	a := newFake(t)
	a.loadSession, a.loadHang = true, true
	cfg := resumeCfg("old")
	cfg.InitTimeout = 300 * time.Millisecond
	_, err := openFake(t, a, cfg)
	if err == nil || !strings.Contains(err.Error(), "session/load") {
		t.Fatalf("want an error naming session/load, got %v", err)
	}
	if _, created := asked(a); created != 0 {
		t.Fatalf("a hung load must not fall through to session/new (created=%d)", created)
	}
}

func TestNoResumeRequestedMeansNoLoad(t *testing.T) {
	a := newFake(t)
	a.loadSession = true
	c, err := openFake(t, a, Config{Name: "fake", Cwd: "/work"})
	if err != nil {
		t.Fatal(err)
	}
	if loaded, created := asked(a); len(loaded) != 0 || created != 1 || c.Resumed() || c.ResumeNote() != "" || !c.CanResume() {
		t.Fatalf("loaded=%v created=%d resumed=%v note=%q", loaded, created, c.Resumed(), c.ResumeNote())
	}
}
