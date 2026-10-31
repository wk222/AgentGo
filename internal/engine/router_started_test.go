package engine

import (
	"context"
	"testing"
)

// The "started" status is the first thing a client hears about a turn; whatever
// the caller put in StartedExtra rides along without disturbing the state key.
func TestStartedStatusCarriesStartedExtra(t *testing.T) {
	r := NewRouter()
	r.Register(&fakeEngine{id: "a", run: func(context.Context, RunRequest, Emitter) (RunResult, error) {
		return RunResult{}, nil
	}})
	extra := map[string]any{"checkpoint_id": "cp_1", "state": "forged"}
	c := &collector{}
	r.Run(context.Background(), RunRequest{SessionID: "s", Input: "x", StartedExtra: extra}, c)

	if len(c.ev) == 0 || c.ev[0].Type != EventStatus {
		t.Fatalf("events: %+v", c.ev)
	}
	p := c.ev[0].Payload
	if p["checkpoint_id"] != "cp_1" {
		t.Fatalf("payload = %#v", p)
	}
	if p["state"] != "started" {
		t.Fatalf("an extra key must not replace the state: %v", p["state"])
	}
	if extra["state"] != "forged" || len(extra) != 2 {
		t.Fatal("the caller's map must not be modified")
	}
}

func TestStartedStatusWithoutExtraIsUnchanged(t *testing.T) {
	r := NewRouter()
	r.Register(&fakeEngine{id: "a", run: func(context.Context, RunRequest, Emitter) (RunResult, error) {
		return RunResult{}, nil
	}})
	c := &collector{}
	r.Run(context.Background(), RunRequest{SessionID: "s", Input: "x"}, c)
	if p := c.ev[0].Payload; len(p) != 1 || p["state"] != "started" {
		t.Fatalf("payload = %#v", p)
	}
}
