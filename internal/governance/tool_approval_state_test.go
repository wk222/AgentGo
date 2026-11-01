package governance

import (
	"bytes"
	"encoding/gob"
	"testing"
)

// ADK checkpoints hold interrupt state as interface values. The concrete
// types seen there must be registered with gob, or saving the checkpoint of a
// run that paused for approval fails and the whole run degrades.
func TestCheckpointInterfaceValuesRoundTrip(t *testing.T) {
	cases := map[string]any{
		"map[string]any":    map[string]any{"tool": "execute_bash", "approval_id": "a1"},
		"[]any":             []any{"a", 1},
		"map[string]string": map[string]string{"call_1": "output"},
		"[]string":          []string{"call_1", "call_2"},
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			in := struct{ V any }{V: v}
			if err := gob.NewEncoder(&buf).Encode(&in); err != nil {
				t.Fatalf("encode %s as interface value: %v", name, err)
			}
			var out struct{ V any }
			if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}
			if out.V == nil {
				t.Fatalf("%s decoded to nil", name)
			}
		})
	}
}
