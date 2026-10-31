package sessions

import (
	"agentgo/internal/promptbudget"
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/eino/schema"
	"strings"
	"testing"
)

func TestStructuredTaskStateSurvivesSerializationAndRepeatedCompression(t *testing.T) {
	msgs := []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("GOAL: keep Eino unchanged"), schema.UserMessage("CONSTRAINT: keep existing tests"), schema.AssistantMessage("Everything is complete", nil)}
	for i := 0; i < 60; i++ {
		msgs = append(msgs, schema.UserMessage(fmt.Sprintf("next request %d", i)), schema.AssistantMessage("reported attempt", nil))
		v := CompileProjectedView(msgs, CompileOptions{MaxActiveTurns: 4, PreserveLatestUser: true})
		if len(v.TaskState) != 1 || promptbudget.Estimate(v.TaskState[0]) > 1400 {
			t.Fatal("missing or oversized state")
		}
		rendered, err := v.Render(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(rendered)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &msgs); err != nil {
			t.Fatal(err)
		}
	}
	text := renderedText(msgs)
	if !strings.Contains(text, "GOAL: keep Eino unchanged") || !strings.Contains(text, "CONSTRAINT: keep existing tests") || strings.Count(text, "<task_state>") != 1 {
		t.Fatal("lost anchor or recursively rendered state")
	}
	var m ProjectionMemory
	for _, msg := range msgs {
		if raw, ok := msg.Extra[extraProjectionMemory].(string); ok {
			if err := json.Unmarshal([]byte(raw), &m); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if m.State == nil || m.State.Goal == "" || len(m.State.Requests) == 0 {
		t.Fatal("typed state did not survive restart")
	}
}

func TestTaskStateEvidenceLabelsAndBoundedRendering(t *testing.T) {
	m := ProjectionMemory{Goal: "Keep tests", UserNotes: []string{"old requirement", "LATEST CORRECTION: build only after tests"}, Entries: []string{"assistant claims tests passed", "\u5de5\u5177\u8fd4\u56de tool-id: exit=1; failed"}}
	state := buildTaskState(m)
	text := state.Prompt(1400)
	for _, want := range []string{"LATEST CORRECTION", "Assistant report (unverified)", "Tool record (verify current state)", "exit=1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	for i := 0; i < 100; i++ {
		state.Requests = append(state.Requests, stateNote("user", strings.Repeat("\u4e2d\u6587", 1000)))
	}
	if promptbudget.Estimate(state.Prompt(1400)) > 1400 {
		t.Fatal("state escaped budget")
	}
}
