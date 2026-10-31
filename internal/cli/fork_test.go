package cli

import (
	"errors"
	"strings"
	"testing"

	"agentgo/internal/engine"
)

// forkingBackend is the scripted brain plus the ability to copy a conversation.
type forkingBackend struct {
	*fakeBackend
	forked []string // sessions asked to fork
	err    error
}

func (f *forkingBackend) Fork(sessionID string) (string, error) {
	f.forked = append(f.forked, sessionID)
	if f.err != nil {
		return "", f.err
	}
	return "fork-of-" + sessionID, nil
}

func replWith(t *testing.T, be Backend, in string) (string, int) {
	t.Helper()
	var out, errOut strings.Builder
	env := IO{In: strings.NewReader(in), Out: &out, Err: &errOut, InTTY: true}
	code := Main(nil, env, func(Config) (Backend, error) { return be, nil })
	return errOut.String(), code
}

func TestReplForkContinuesInTheCopy(t *testing.T) {
	be := &forkingBackend{fakeBackend: newFake()}
	be.runs = []func(engine.EventSink) engine.RunResult{say("one"), say("two"), say("three")}
	stderr, code := replWith(t, be, "first\n/fork\nsecond\n/fork\nthird\n/help\n/exit\n")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	// The first message creates s1; every later message goes to the newest copy.
	want := []string{"s1|first", "fork-of-s1|second", "fork-of-fork-of-s1|third"}
	if strings.Join(be.inputs, ",") != strings.Join(want, ",") {
		t.Fatalf("inputs = %q, want %q", be.inputs, want)
	}
	if strings.Join(be.forked, ",") != "s1,fork-of-s1" {
		t.Fatalf("forked = %v", be.forked)
	}
	if !strings.Contains(stderr, "forked s1 -> session fork-of-s1") || !strings.Contains(stderr, "the original is unchanged") {
		t.Fatalf("stderr: %s", stderr)
	}
	if !strings.Contains(stderr, "/fork") {
		t.Fatal("/fork missing from the help text")
	}
}

func TestReplForkSaysWhyItCannot(t *testing.T) {
	// Nothing to copy yet.
	be := &forkingBackend{fakeBackend: newFake()}
	stderr, _ := replWith(t, be, "/fork\n/exit\n")
	if !strings.Contains(stderr, "nothing to fork yet") || len(be.forked) != 0 {
		t.Fatalf("empty session: %s %v", stderr, be.forked)
	}

	// The backend refuses (a run in flight, say): the session stays where it was.
	be = &forkingBackend{fakeBackend: newFake(), err: errors.New("这条对话正在运行")}
	be.runs = []func(engine.EventSink) engine.RunResult{say("one"), say("two")}
	stderr, _ = replWith(t, be, "first\n/fork\nsecond\n/exit\n")
	if !strings.Contains(stderr, "error: 这条对话正在运行") {
		t.Fatalf("refusal not shown: %s", stderr)
	}
	if strings.Join(be.inputs, ",") != "s1|first,s1|second" {
		t.Fatalf("a failed fork moved the session: %q", be.inputs)
	}

	// A backend that cannot fork at all.
	plain := newFake()
	plain.runs = []func(engine.EventSink) engine.RunResult{say("one")}
	stderr, _ = replWith(t, plain, "first\n/fork\n/exit\n")
	if !strings.Contains(stderr, "cannot fork") {
		t.Fatalf("plain backend: %s", stderr)
	}
}
