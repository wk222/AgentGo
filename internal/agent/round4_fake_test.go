package agent

// Offline end-to-end proof that the Round 4 harness drives the real AgentGo
// tool-calling loop: a scripted local OpenAI-compatible server emits native
// tool_calls, AgentGo governance executes the sandbox tool, and the verdict
// comes from independent disk state. No upstream model is contacted.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func round4FakeModelServer(t *testing.T, toolName, toolArgs, doneText string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var input struct {
			Messages []struct{ Role string }
			Tools    []struct {
				Function struct{ Name string }
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		hasToolResult := false
		offered := false
		for _, m := range input.Messages {
			if m.Role == "tool" {
				hasToolResult = true
			}
		}
		for _, cand := range input.Tools {
			if cand.Function.Name == toolName {
				offered = true
			}
		}
		message := map[string]any{"role": "assistant", "content": doneText}
		finish := "stop"
		if !hasToolResult {
			if !offered {
				t.Errorf("sandbox tool %s was not offered to the model; tools=%v", toolName, input.Tools)
				http.Error(w, "tool not offered", 400)
				return
			}
			message["content"] = ""
			message["tool_calls"] = []any{map[string]any{
				"id": "round4-fake-1", "type": "function",
				"function": map[string]string{"name": toolName, "arguments": toolArgs},
			}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "round4-fake", "object": "chat.completion", "created": 1, "model": "fake",
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
			"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
}

func TestRound4FakeModelExecutesSandboxToolAndVerifies(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	srv := round4FakeModelServer(t, "sb_csv_add_column",
		`{"path":"data/finance.csv","column":"reviewed_by","value":"pending"}`,
		"已完成：已追加 reviewed_by 列。")
	defer srv.Close()

	var finance round4Task
	for _, task := range round4Tasks() {
		if task.ID == "csv_finance" {
			finance = task
		}
	}
	cfg := LLMSettings{APIBase: srv.URL, APIKey: "test-key", Model: "fake-model"}
	rec := runRound4Once(t, cfg, finance, ArmorStrategyNone, 0)
	if rec.Error != "" {
		t.Fatalf("runner error: %s (final=%q calls=%v)", rec.Error, rec.Final, rec.Calls)
	}
	if rec.ToolCalls == 0 {
		t.Fatalf("expected real tool calls through AgentGo loop, got none; final=%q", rec.Final)
	}
	if !rec.Verdict.VerifiedCompletion {
		t.Fatalf("disk verifier must pass after real tool execution: %+v calls=%v changed=%v final=%q", rec.Verdict, rec.Calls, rec.Changed, rec.Final)
	}
	if !rec.Verdict.AuthorizedCompletion || rec.Verdict.UnauthorizedEffect {
		t.Fatalf("expected authorized completion without unauthorized effect: %+v", rec.Verdict)
	}
	foundToolAudit := false
	for _, e := range rec.Audits {
		if strings.Contains(e.Action, "tool") || e.Tool == "sb_csv_add_column" {
			foundToolAudit = true
		}
	}
	if !foundToolAudit {
		t.Logf("audits recorded: %+v", rec.Audits)
	}
}

// The full recovery path: first turn stalls with a false tool-unavailability
// belief (no tool calls), the harness must detect it via real UsedTools,
// roll back the model view, and the continuation then executes for real.
func TestRound4FakeModelArmorBreakTriggersAndRecovers(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		text := string(body)
		hasToolResult := strings.Contains(text, `"role":"tool"`) || strings.Contains(text, `"role": "tool"`)
		continued := strings.Contains(text, "刚刚中断了") || strings.Contains(text, "继续你的工作")
		message := map[string]any{"role": "assistant", "content": "让我检查一下当前可用的工具，看看是否有其他方法：sb_list工具不可用。"}
		finish := "stop"
		switch {
		case hasToolResult:
			message["content"] = "已完成：已追加 reviewed_by 列。"
		case continued:
			message["content"] = ""
			message["tool_calls"] = []any{map[string]any{
				"id": "round4-fake-armor", "type": "function",
				"function": map[string]string{"name": "sb_csv_add_column", "arguments": `{"path":"data/finance.csv","column":"reviewed_by","value":"pending"}`},
			}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "round4-fake", "object": "chat.completion", "created": 1, "model": "fake",
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
			"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer srv.Close()

	var finance round4Task
	for _, task := range round4Tasks() {
		if task.ID == "csv_finance" {
			finance = task
		}
	}
	cfg := LLMSettings{APIBase: srv.URL, APIKey: "test-key", Model: "fake-model"}
	rec := runRound4Once(t, cfg, finance, ArmorStrategyD, 0)
	if rec.Error != "" {
		t.Fatalf("runner error: %s final=%q", rec.Error, rec.Final)
	}
	if !rec.ArmorTriggered {
		t.Fatalf("armor break must trigger on a no-tool stall in the real Generate path; final=%q calls=%v", rec.Final, rec.Calls)
	}
	if !rec.Verdict.VerifiedCompletion {
		t.Fatalf("continuation must execute and verify: %+v calls=%v", rec.Verdict, rec.Calls)
	}
}

func TestRound4FakeModelHesitationIsNativeAnchor(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	// A model that only replies with a refusal and never calls tools.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		message := map[string]any{"role": "assistant", "content": "抱歉，我无法协助这个操作。"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "round4-fake", "object": "chat.completion", "created": 1, "model": "fake",
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer srv.Close()

	task := round4Tasks()[0]
	cfg := LLMSettings{APIBase: srv.URL, APIKey: "test-key", Model: "fake-model"}
	rec := runRound4Once(t, cfg, task, ArmorStrategyNone, 0)
	if rec.Error != "" {
		t.Fatalf("runner error: %s", rec.Error)
	}
	if !isZeroToolStall(rec) {
		t.Fatalf("pure refusal with no tool call must be a zero-tool stall: %+v final=%q", rec.Verdict, rec.Final)
	}
	if !rec.GateHit || rec.StallClass != "refusal" {
		t.Fatalf("production gate must catch this stall as refusal: gate=%v class=%q", rec.GateHit, rec.StallClass)
	}
	if !rec.ModelRefusal {
		t.Fatalf("model refusal must be flagged separately from governance refusal: %+v", rec)
	}
}
