package tools

import (
	"bytes"
	"encoding/gob"
	"testing"

	"agentgo/internal/interactive"
)

// The interrupt info of ask_user is checkpointed as an interface value, so its
// concrete type must be gob-registered. Without that, a real ask_user call
// failed with "type not registered for interface: interactive.QuestionPayload".
func TestAskUserQuestionPayloadSurvivesCheckpointEncoding(t *testing.T) {
	type box struct{ Info any }
	in := box{Info: interactive.QuestionPayload{
		Tool: "ask_user", Prompt: "red or blue?", QuestionID: "q1",
		Choices: []interactive.Choice{{ID: "red", Label: "Red"}},
	}}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(in); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var out box
	if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	q, ok := out.Info.(interactive.QuestionPayload)
	if !ok || q.QuestionID != "q1" || len(q.Choices) != 1 {
		t.Fatalf("round trip lost the payload: %#v", out.Info)
	}
}
