package workflow

import (
	"context"
	"testing"
)

func TestWaitSignalCheckpointResume(t *testing.T) {
	store := &memCheckPointStore{m: make(map[string][]byte)}
	def := Definition{
		ID:   "wait-cp",
		Name: "wait-cp",
		Nodes: []Node{
			{ID: "n1", Type: "input"},
			{ID: "n2", Type: "wait_signal", Config: map[string]any{"signal": "tick"}},
			{ID: "n3", Type: "output"},
		},
		Edges: []Edge{
			{From: "n1", To: "n2"},
			{From: "n2", To: "n3"},
		},
	}
	rc := &RunContext{
		Vars:            map[string]string{},
		CheckPointStore: store,
		RunID:           "r1",
		CheckPointID:    CheckpointID("wait-cp", "r1"),
		Input:           "in",
	}
	ctx := context.Background()
	_, err := Execute(ctx, def, *rc)
	ie, ok := AsInterrupt(err)
	if !ok {
		t.Fatalf("want interrupt, err=%v", err)
	}
	out, err := ResumeExecute(ctx, def, *rc, ie.InterruptID, waitResumeData{Payload: "pong"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "pong" {
		t.Fatalf("got %q", out)
	}
}
