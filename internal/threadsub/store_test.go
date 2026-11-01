package threadsub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sub", "threads.json")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func TestBindResolveAndSurviveARestart(t *testing.T) {
	s, p := open(t)
	if _, err := s.Bind("github:acme/app#12", "sess-1", "PR 12"); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Resolve("github:acme/app#12")
	if !ok || got.SessionID != "sess-1" || got.Title != "PR 12" {
		t.Fatalf("binding lost across a restart: %+v ok=%v", got, ok)
	}
}

func TestKeyIsTrimmedAndRejectedWhenUnusable(t *testing.T) {
	s, _ := open(t)
	if _, err := s.Bind("  k  ", "s", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Resolve("k"); !ok {
		t.Fatal("a padded key should match its trimmed form")
	}
	for _, bad := range []string{"", "   ", "a\nb", strings.Repeat("x", maxKeyLen+1)} {
		if _, err := s.Bind(bad, "s", ""); err == nil {
			t.Fatalf("key %q was accepted", bad)
		}
	}
	if _, err := s.Bind("k2", " ", ""); err == nil {
		t.Fatal("an empty session id was accepted")
	}
}

func TestRebindingHandsTheThreadOver(t *testing.T) {
	s, _ := open(t)
	_, _ = s.Bind("k", "old", "t")
	prev, err := s.Bind("k", "new", "")
	if err != nil || prev != "old" {
		t.Fatalf("prev=%q err=%v", prev, err)
	}
	got, _ := s.Resolve("k")
	if got.SessionID != "new" || got.Title != "t" {
		t.Fatalf("handover lost the title or the owner: %+v", got)
	}
}

func TestOnlyTheOwnerMayUnbind(t *testing.T) {
	s, _ := open(t)
	_, _ = s.Bind("k", "owner", "")
	if err := s.Unbind("k", "someone-else"); err == nil {
		t.Fatal("another session removed the binding")
	}
	if err := s.Unbind("k", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Resolve("k"); ok {
		t.Fatal("binding still there")
	}
	if err := s.Unbind("k", ""); err == nil {
		t.Fatal("removing a missing binding should say so")
	}
}

func TestPerSessionLimit(t *testing.T) {
	s, _ := open(t)
	for i := 0; i < maxPerSession; i++ {
		if _, err := s.Bind("k"+string(rune('A'+i%26))+string(rune('a'+i/26)), "s", ""); err != nil {
			t.Fatalf("binding %d: %v", i, err)
		}
	}
	if _, err := s.Bind("one-too-many", "s", ""); err == nil {
		t.Fatal("the session limit was not enforced")
	}
	if _, err := s.Bind("other", "s2", ""); err != nil {
		t.Fatal("another session should not be affected:", err)
	}
}

func TestDeliveredCountsAndListOrdersByActivity(t *testing.T) {
	s, _ := open(t)
	_, _ = s.Bind("a", "s", "")
	_, _ = s.Bind("b", "s", "")
	_, _ = s.Bind("c", "other", "")
	s.Delivered("b")
	s.Delivered("b")
	l := s.List("s")
	if len(l) != 2 || l[0].Key != "b" || l[0].Events != 2 {
		t.Fatalf("list should put the active thread first: %+v", l)
	}
	if len(s.List("")) != 3 {
		t.Fatal("an empty session lists everything")
	}
}

func TestSessionForIsStableAndSafe(t *testing.T) {
	a := SessionFor("github:acme/app#12")
	if a != SessionFor("github:acme/app#12") || !strings.HasPrefix(a, "thread:") {
		t.Fatalf("not stable: %q", a)
	}
	if strings.ContainsAny(a, "/:\\ ") && !strings.HasPrefix(a, "thread:") {
		t.Fatalf("unsafe characters: %q", a)
	}
	if strings.ContainsAny(strings.TrimPrefix(a, "thread:"), "/:\\ ") {
		t.Fatalf("unsafe characters in %q", a)
	}
	if len(SessionFor(strings.Repeat("x", 200))) > maxSessionIDSize {
		t.Fatal("too long")
	}
}

func TestCorruptFileIsReportedNotOverwritten(t *testing.T) {
	p := filepath.Join(t.TempDir(), "threads.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p); err == nil {
		t.Fatal("a corrupt file must not be treated as empty (it would be overwritten)")
	}
}
