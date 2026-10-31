package tui

import (
	"errors"
	"testing"

	"agentgo/internal/cli"
)

// forkBE is the scripted backend plus the ability to copy a conversation.
type forkBE struct {
	*fakeBE
	forked []string
	err    error
}

func (f *forkBE) Fork(sessionID string) (string, error) {
	f.forked = append(f.forked, sessionID)
	if f.err != nil {
		return "", f.err
	}
	return "fork-1", nil
}

func TestForkCommandSwitchesToTheCopyAndShowsItsHistory(t *testing.T) {
	inner := &fakeBE{
		run: answerRun("done"),
		history: map[string][]cli.Message{"fork-1": {
			{Role: "user", Type: "text", Content: "the question before the fork"},
			{Role: "assistant", Type: "text", Content: "the answer before the fork"},
		}},
	}
	be := &forkBE{fakeBE: inner}
	h := newHarness(t, be, Options{})

	h.say("/fork")
	h.mustView("nothing to fork yet")
	if len(be.forked) != 0 {
		t.Fatal("forked an empty conversation")
	}

	h.say("hello") // creates sess-1
	h.say("/fork")
	if h.m.session != "fork-1" || h.m.mode != modeIdle {
		t.Fatalf("session=%q mode=%v", h.m.session, h.m.mode)
	}
	if len(be.forked) != 1 || be.forked[0] != "sess-1" {
		t.Fatalf("forked %v", be.forked)
	}
	h.mustView("forked from sess-1")
	h.mustView("the question before the fork")
	h.mustView("the answer before the fork")
	h.say("/help")
	h.mustView("/fork")
}

func TestForkCommandReportsARefusalAndStaysPut(t *testing.T) {
	be := &forkBE{fakeBE: &fakeBE{run: answerRun("done")}, err: errors.New("这条对话停在等你批准或回答")}
	h := newHarness(t, be, Options{})
	h.say("hello")
	h.say("/fork")
	if h.m.session != "sess-1" {
		t.Fatalf("a failed fork moved the session to %q", h.m.session)
	}
	h.mustView("这条对话停在等你批准或回答")
}

func TestForkCommandNeedsABackendThatCanFork(t *testing.T) {
	h := newHarness(t, &fakeBE{run: answerRun("done")}, Options{})
	h.say("hello")
	h.say("/fork")
	h.mustView("cannot fork")
	if h.m.session != "sess-1" {
		t.Fatalf("session=%q", h.m.session)
	}
}
