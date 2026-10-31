package agent

// Reproducibility aid: write the exact first chat-completions request AgentGo
// sends for a Round 4 task (system prompt, task, tool schemas) to a file, using
// a local fake upstream so no credential or network is involved. The file can
// then be replayed straight at a gateway to separate AgentGo's context from the
// gateway/model behaviour. It never contains an API key.

import (
	"os"
	"testing"
)

func TestRound4DumpFirstRequest(t *testing.T) {
	out := os.Getenv("ROUND4_DUMP_REQ")
	if out == "" {
		t.Skip("set ROUND4_DUMP_REQ=<file> (and optionally ROUND4_DUMP_TASK=<task id>) to dump the first request")
	}
	id := os.Getenv("ROUND4_DUMP_TASK")
	if id == "" {
		id = "e_kill_terse"
	}
	task, ok := round4TaskByID(id)
	if !ok {
		t.Fatalf("unknown task %q", id)
	}
	up := newFakeUpstream(t, func(body string) map[string]any {
		return map[string]any{"role": "assistant", "content": "ok"}
	})
	cfg := LLMSettings{APIBase: up.srv.URL, APIKey: "dump-only-not-a-key", Model: "deepseek-v4-flash"}
	rec := runRound4(t, round4RunSpec{Cfg: cfg, Task: task, Strategy: ArmorStrategyNone})
	if rec.HarnessError != "" {
		t.Fatalf("harness error: %s", rec.HarnessError)
	}
	bodies := up.Bodies()
	if len(bodies) == 0 {
		t.Fatal("no request reached the fake upstream")
	}
	if err := os.WriteFile(out, []byte(bodies[0]), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote first request (%d bytes) for task %s to %s", len(bodies[0]), id, out)
}
