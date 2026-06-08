package workflow

import (
	"context"
	"testing"
)

func TestAskUserCheckpointResume(t *testing.T) {
	store := &memCheckPointStore{m: make(map[string][]byte)}
	def := Definition{
		ID: "ask-cp", Name: "ask-cp",
		Nodes: []Node{
			{ID: "n1", Type: "input"},
			{ID: "n2", Type: "ask_user", Prompt: "Your name?"},
			{ID: "n3", Type: "output"},
		},
		Edges: []Edge{{From: "n1", To: "n2"}, {From: "n2", To: "n3"}},
	}
	rc := &RunContext{
		Vars: map[string]string{}, CheckPointStore: store,
		RunID: "r1", CheckPointID: CheckpointID("ask-cp", "r1"), Input: "in",
	}
	_, err := Execute(context.Background(), def, *rc)
	ie, ok := AsInterrupt(err)
	if !ok {
		t.Fatalf("want interrupt err=%v", err)
	}
	out, err := ResumeExecute(context.Background(), def, *rc, ie.InterruptID, waitResumeData{Payload: "Alice"})
	if err != nil || out != "Alice" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
