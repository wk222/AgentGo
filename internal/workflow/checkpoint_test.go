package workflow

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/compose"
)

type memCheckPointStore struct {
	m map[string][]byte
}

func (s *memCheckPointStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	b, ok := s.m[id]
	return b, ok, nil
}

func (s *memCheckPointStore) Set(_ context.Context, id string, b []byte) error {
	s.m[id] = b
	return nil
}

func TestWorkflowCheckpointResume(t *testing.T) {
	store := &memCheckPointStore{m: make(map[string][]byte)}
	def := Definition{
		ID:   "cp-test",
		Name: "cp-test",
		Nodes: []Node{
			{ID: "n1", Type: "code", Prompt: "one"},
			{ID: "n2", Type: "code", Prompt: "two"},
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
		RunID:           "run1",
		CheckPointID:    CheckpointID("cp-test", "run1"),
		Input:           "hello",
	}
	ctx := context.Background()
	runnable, err := CompileToCompose(ctx, def, rc, compose.WithInterruptAfterNodes([]string{"n1"}))
	if err != nil {
		t.Fatal(err)
	}
	state := &WorkflowState{OriginalInput: rc.Input, Vars: rc.Vars}
	_, err = runnable.Invoke(ctx, state, compose.WithCheckPointID(rc.CheckPointID))
	if err == nil {
		t.Fatal("expected interrupt after n1")
	}
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok || len(info.InterruptContexts) == 0 {
		t.Fatalf("interrupt info: ok=%v err=%v", ok, err)
	}
	iid := info.InterruptContexts[0].ID
	out, err := ResumeExecute(ctx, def, *rc, iid, map[string]string{"resume": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("expected output after resume")
	}
}
