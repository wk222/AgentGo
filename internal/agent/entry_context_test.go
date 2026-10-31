package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentgo/internal/checkpoint"
	"agentgo/internal/evidence"
	"agentgo/internal/governance"
	"agentgo/internal/tools"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type entryProbeKey struct{}

// A local protocol server exercises the actual entry points and Eino tools;
// it does not contact an upstream model or score model quality.
func entryModelServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var input struct {
			Messages []struct{ Role string }
			Tools    []struct{ Function struct{ Name string } }
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		message := map[string]any{"role": "assistant", "content": "done"}
		finish := "stop"
		hasResult := false
		for _, m := range input.Messages {
			if m.Role == "tool" {
				hasResult = true
			}
		}
		if !hasResult {
			name := ""
			args := "{}"
			for _, candidate := range input.Tools {
				switch candidate.Function.Name {
				case "workflow_agent":
					name = "workflow_agent"
					args = `{"request":"run the workflow"}`
				case "run_workflow":
					name = "run_workflow"
				case "read_inner_app_file":
					name = "read_inner_app_file"
				}
			}
			if name == "" {
				t.Error("expected test tool not offered")
				http.Error(w, "missing tool", 400)
				return
			}
			message["content"] = ""
			message["tool_calls"] = []any{map[string]any{"id": "entry-" + name, "type": "function", "function": map[string]string{"name": name, "arguments": args}}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "entry-test", "object": "chat.completion", "created": 1, "model": "ctx-test", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
	}))
}

func contextEntryRunner(t *testing.T, registry *tools.Registry, db *sql.DB) *Runner {
	t.Helper()
	cp, err := checkpoint.OpenSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	return &Runner{cpStore: cp, toolReg: registry, runControl: NewRunControl(), workspaceRoot: root, dataDir: root}
}

func assertEntryContext(t *testing.T, ctx context.Context, sid string) {
	t.Helper()
	if SessionIDFromContext(ctx) != sid || governance.SessionIDFromContext(ctx) != sid {
		t.Errorf("split context: agent=%s governance=%s want=%s", SessionIDFromContext(ctx), governance.SessionIDFromContext(ctx), sid)
	}
	if ctx.Value(entryProbeKey{}) != "parent" || governance.SubagentDepth(ctx) < 2 {
		t.Error("parent context or depth lost")
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Error("deadline lost")
	}
	c := evidence.Register(ctx, sid, evidence.Citation{Kind: "file", Source: "fixture", Revision: "r", End: 2}, "证据")
	if c == nil {
		t.Error("evidence scope missing")
	}
}

func entryParent(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ctx = context.WithValue(ctx, entryProbeKey{}, "parent")
	return governance.WithSubagentDepth(BindRunContext(ctx, "parent"), 2), cancel
}

func TestAppBuilderEntryScopesAndCancellation(t *testing.T) {
	t.Setenv("AGENTGO_LLM_API", "chat")
	server := entryModelServer(t)
	defer server.Close()
	for _, cancelRun := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelRun), func(t *testing.T) {
			registry := tools.NewRegistry()
			entered := make(chan struct{}, 1)
			reader, err := utils.InferTool("read_inner_app_file", "test", func(ctx context.Context, _ struct{}) (string, error) {
				assertEntryContext(t, ctx, "builder-session")
				entered <- struct{}{}
				if cancelRun {
					<-ctx.Done()
					return "", ctx.Err()
				}
				return "source", nil
			})
			if err != nil {
				t.Fatal(err)
			}
			registry.AddTool(reader)
			_, db := testRuntimeStore(t, filepath.Join(t.TempDir(), "builder.db"))
			r := contextEntryRunner(t, registry, db)
			r.queue = newPipelineTestRunner(t).queue
			obs, meter := &recObs{}, &RunMeter{}
			hookCalls := 0
			var hookErr error
			dispose := governance.RegisterToolHook(governance.ToolHook{Name: "builder-entry", After: func(ctx context.Context, call governance.ToolCall, outcome governance.ToolOutcome) {
				hookCalls++
				hookErr = outcome.Err
				if call.Name != "read_inner_app_file" || governance.SessionIDFromContext(ctx) != "builder-session" {
					t.Error("builder bypassed scoped tool execution")
				}
			}})
			defer dispose()
			ctx, cancel := entryParent(t)
			defer cancel()
			ctx = WithRunMeter(WithToolObserver(ctx, obs), meter)
			done := make(chan error, 1)
			go func() {
				_, err := r.RunAppBuilderAgent(ctx, LLMSettings{APIBase: server.URL + "/v1", APIKey: "test", Model: "ctx-test"}, " builder-session ", "read source")
				done <- err
			}()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("entry finished before tool: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if cancelRun {
				cancel()
			}
			select {
			case err := <-done:
				if !cancelRun && err != nil {
					t.Fatal(err)
				}
				if cancelRun && err == nil {
					t.Fatal("cancelled run reported success")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation did not finish")
			}
			if r.runControl.IsRunning("builder-session") {
				t.Fatal("builder left a stale cancel handle")
			}
			if hookCalls != 1 || len(obs.lines) != 2 || meter.Snapshot().ToolCalls != 1 || (cancelRun && hookErr == nil) || (!cancelRun && hookErr != nil) {
				t.Fatalf("builder execution semantics differ: hooks=%d err=%v events=%v meter=%+v", hookCalls, hookErr, obs.lines, meter.Snapshot())
			}
		})
	}
}

func TestMatrixResumeKeepsSessionAfterCheckpointReopen(t *testing.T) {
	t.Setenv("AGENTGO_LLM_API", "chat")
	server := entryModelServer(t)
	defer server.Close()
	registry := tools.NewRegistry()
	var executed atomic.Int32
	workflow, err := utils.InferTool("run_workflow", "test", func(ctx context.Context, _ struct{}) (string, error) {
		assertEntryContext(t, ctx, "matrix-user")
		target, has, payload := tool.GetResumeContext[*governance.ResumePayload](ctx)
		if target && has && payload != nil && payload.Approved {
			executed.Add(1)
			return "executed", nil
		}
		return "", tool.StatefulInterrupt(ctx, governance.ToolApprovalPause{ApprovalID: "matrix-approval", ToolName: "run_workflow", Arguments: "{}"}, "pending")
	})
	if err != nil {
		t.Fatal(err)
	}
	registry.AddTool(workflow)
	list, err := utils.InferTool("list_inner_apps", "test", func(context.Context, struct{}) (string, error) { return "[]", nil })
	if err != nil {
		t.Fatal(err)
	}
	registry.AddTool(list)
	path := filepath.Join(t.TempDir(), "matrix.db")
	_, db := testRuntimeStore(t, path)
	r := contextEntryRunner(t, registry, db)
	ctx, cancel := entryParent(t)
	defer cancel()
	cfg := LLMSettings{APIBase: server.URL + "/v1", APIKey: "test", Model: "ctx-test"}
	paused, err := r.RunMatrixSupervisor(ctx, cfg, "matrix-user", "run workflow")
	if err != nil || paused.PendingApproval == nil {
		t.Fatalf("did not pause: %+v %v", paused, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.SetMaxOpenConns(1)
	resumed := contextEntryRunner(t, registry, reopened)
	result, err := resumed.ResumeMatrixSupervisor(ctx, cfg, "matrix-user", paused.PendingApproval.InterruptID, &governance.ResumePayload{Approved: true})
	if err != nil || result.PendingApproval != nil || !strings.Contains(result.Content, "done") || executed.Load() != 1 {
		t.Fatalf("resume failed/duplicated: %+v count=%d %v", result, executed.Load(), err)
	}
	if resumed.runControl.IsRunning("matrix-user") {
		t.Fatal("resumed run left stale handle")
	}
}
