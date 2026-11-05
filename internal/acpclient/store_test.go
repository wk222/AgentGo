package acpclient

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func rec(label string, at time.Time) savedSession {
	return savedSession{Label: label, Agent: "fake", Cwd: "/w", SessionID: "id-" + label, Updated: at}
}

func TestStoreRoundTrip(t *testing.T) {
	s := newSessionStore(storePath(t))
	now := time.Now()
	for i, l := range []string{"a", "b", "c"} {
		if err := s.put(rec(l, now.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	got := s.list()
	if len(got) != 3 || got[0].Label != "c" || got[2].Label != "a" {
		t.Fatalf("newest first: %+v", got)
	}
	if r, ok := s.get("b"); !ok || r.SessionID != "id-b" {
		t.Fatalf("%+v %v", r, ok)
	}
	if err := s.put(savedSession{Label: "b", Agent: "fake", Cwd: "/other", SessionID: "again", Updated: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.get("b"); r.SessionID != "again" || len(s.list()) != 3 {
		t.Fatalf("a label has one record, the newest: %+v", s.list())
	}
	if !s.delete("b") || s.delete("b") {
		t.Fatal("delete must report whether there was a record")
	}
	if len(s.list()) != 2 {
		t.Fatalf("%+v", s.list())
	}
}

func TestStoreToleratesAMissingOrDamagedFile(t *testing.T) {
	path := storePath(t)
	s := newSessionStore(path)
	if got := s.list(); got != nil {
		t.Fatalf("a missing file is an empty store: %+v", got)
	}
	for name, content := range map[string]string{
		"garbage":       "this is not json",
		"truncated":     `{"version":1,"sessions":[{"label":"a"`,
		"newer version": `{"version":99,"sessions":[{"label":"a","session_id":"x","updated":"2999-01-01T00:00:00Z"}]}`,
		"empty":         "",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := s.list(); len(got) != 0 {
			t.Errorf("%s: %+v", name, got)
		}
		if err := s.put(rec("fresh", time.Now())); err != nil {
			t.Errorf("%s: a damaged file must be replaceable: %v", name, err)
		}
		if got := s.list(); len(got) != 1 || got[0].Label != "fresh" {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestStoreSkipsIncompleteRecords(t *testing.T) {
	path := storePath(t)
	s := newSessionStore(path)
	data := fmt.Sprintf(`{"version":1,"sessions":[{"label":"","session_id":"x","updated":%q},{"label":"nid","updated":%q},{"label":"ok","session_id":"y","updated":%q}]}`,
		time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.list(); len(got) != 1 || got[0].Label != "ok" {
		t.Fatalf("a record that cannot be carried on is not worth listing: %+v", got)
	}
}

func TestStoreKeepsOnlyTheNewest(t *testing.T) {
	s := newSessionStore(storePath(t))
	base := time.Now().Add(-time.Hour)
	for i := 0; i < maxSaved+5; i++ {
		if err := s.put(rec(fmt.Sprintf("l%02d", i), base.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	got := s.list()
	if len(got) != maxSaved || got[0].Label != fmt.Sprintf("l%02d", maxSaved+4) {
		t.Fatalf("len=%d first=%q", len(got), got[0].Label)
	}
	if _, ok := s.get("l00"); ok {
		t.Fatal("the oldest must have been dropped")
	}
}

func TestStoreForgetsOldSessions(t *testing.T) {
	s := newSessionStore(storePath(t))
	t0 := time.Now()
	s.now = func() time.Time { return t0 }
	if err := s.put(rec("old", t0)); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return t0.Add(savedTTL - time.Hour) }
	if len(s.list()) != 1 {
		t.Fatal("a session inside the lifetime is kept")
	}
	s.now = func() time.Time { return t0.Add(savedTTL + time.Hour) }
	if got := s.list(); len(got) != 0 {
		t.Fatalf("the agent has forgotten it by now too: %+v", got)
	}
}

func TestStoreRejectsBadRecords(t *testing.T) {
	s := newSessionStore(storePath(t))
	for name, r := range map[string]savedSession{
		"no label":      {SessionID: "x"},
		"no session id": {Label: "a"},
		"long label":    {Label: strings.Repeat("长", maxLabelLen+1), SessionID: "x"},
	} {
		if s.put(r) == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if err := s.put(savedSession{Label: strings.Repeat("长", maxLabelLen), SessionID: "x"}); err != nil {
		t.Fatalf("the limit counts characters, not bytes: %v", err)
	}
}
