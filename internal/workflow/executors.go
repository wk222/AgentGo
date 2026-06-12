package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// StartNodeExecutor returns the input.
type StartNodeExecutor struct{}

func (e *StartNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	return rc.Input, nil
}

// EndNodeExecutor returns the last output.
type EndNodeExecutor struct{}

func (e *EndNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	return last, nil
}

// LLMNodeExecutor generates text using the LLM.
type LLMNodeExecutor struct{}

func (e *LLMNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	prompt := n.Prompt
	if prompt == "" {
		prompt = last
	}
	prompt = expandTemplateVars(prompt, input, last, rc.Vars)
	if rc.LLMGenerate == nil {
		return "", fmt.Errorf("llm node %s: no LLM configured", n.ID)
	}
	return rc.LLMGenerate(ctx, prompt)
}

// ToolNodeExecutor invokes a tool.
type ToolNodeExecutor struct{}

func (e *ToolNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	args := n.ArgsJSON
	if args == "" {
		args = fmt.Sprintf(`{"input":%q}`, last)
	}
	return rc.InvokeTool(ctx, n.ToolName, args)
}

// CodeNodeExecutor runs a simple string template/transform.
type CodeNodeExecutor struct{}

func (e *CodeNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	if n.Prompt != "" {
		return expandTemplateVars(n.Prompt, input, last, rc.Vars), nil
	}
	if n.Config != nil {
		if tpl, ok := n.Config["template"].(string); ok {
			return expandTemplateVars(tpl, input, last, rc.Vars), nil
		}
		if expr, ok := n.Config["concat"].(string); ok {
			return expandTemplateVars(expr, input, last, rc.Vars), nil
		}
	}
	return last, nil
}

// ConditionNodeExecutor passes the output through unchanged (routing logic is handled by pickNextEdge).
type ConditionNodeExecutor struct{}

func (e *ConditionNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	return last, nil
}

// HTTPNodeExecutor makes an HTTP request.
type HTTPNodeExecutor struct{}

func (e *HTTPNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	url := n.Prompt
	method := http.MethodGet
	body := ""
	if n.Config != nil {
		if u, ok := n.Config["url"].(string); ok && u != "" {
			url = u
		}
		if m, ok := n.Config["method"].(string); ok && m != "" {
			method = strings.ToUpper(m)
		}
		if b, ok := n.Config["body"].(string); ok {
			body = expandTemplate(b, input, last)
		}
	}
	url = expandTemplate(url, input, last)
	if url == "" {
		return "", fmt.Errorf("http node %s: missing url", n.ID)
	}
	reqCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, url, bodyReader)
	if err != nil {
		return "", err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(b)), nil
}

// VariableNodeExecutor sets workflow variables ({{var.name}} in templates).
type VariableNodeExecutor struct{}

func (e *VariableNodeExecutor) Execute(_ context.Context, n Node, input, last string, rc RunContext) (string, error) {
	if rc.Vars == nil {
		return last, nil
	}
	if n.Config != nil {
		if name, ok := n.Config["name"].(string); ok {
			val := ""
			if v, ok := n.Config["value"].(string); ok {
				val = expandTemplateVars(v, input, last, rc.Vars)
			} else {
				val = last
			}
			rc.Vars[name] = val
		}
		for k, v := range n.Config {
			if k == "name" || k == "value" {
				continue
			}
			if s, ok := v.(string); ok {
				rc.Vars[k] = expandTemplateVars(s, input, last, rc.Vars)
			}
		}
	}
	return last, nil
}

// JSONNodeExecutor extracts a JSON field from last output.
type JSONNodeExecutor struct{}

func (e *JSONNodeExecutor) Execute(_ context.Context, n Node, input, last string, rc RunContext) (string, error) {
	field := "result"
	if n.Config != nil {
		if f, ok := n.Config["field"].(string); ok && f != "" {
			field = f
		}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(last)), &m); err != nil {
		return "", fmt.Errorf("json node %s: %w", n.ID, err)
	}
	if v, ok := m[field]; ok {
		switch t := v.(type) {
		case string:
			return t, nil
		default:
			b, _ := json.Marshal(t)
			return string(b), nil
		}
	}
	return "", fmt.Errorf("json node %s: field %q not found", n.ID, field)
}

// DelayNodeExecutor sleeps for configured milliseconds.
type DelayNodeExecutor struct{}

func (e *DelayNodeExecutor) Execute(ctx context.Context, n Node, _ string, last string, _ RunContext) (string, error) {
	ms := 500
	if n.Config != nil {
		switch v := n.Config["ms"].(type) {
		case float64:
			ms = int(v)
		case int:
			ms = v
		}
	}
	if ms > 0 {
		t := time.NewTimer(time.Duration(ms) * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return last, ctx.Err()
		case <-t.C:
		}
	}
	return last, nil
}

// SubworkflowNodeExecutor runs another saved workflow by id.
type SubworkflowNodeExecutor struct{}

func (e *SubworkflowNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	if rc.RunSubworkflow == nil {
		return "", fmt.Errorf("subworkflow node %s: runner unavailable", n.ID)
	}
	wid := strings.TrimSpace(n.ToolName)
	if wid == "" && n.Config != nil {
		if id, ok := n.Config["workflow_id"].(string); ok {
			wid = id
		}
	}
	if wid == "" {
		return "", fmt.Errorf("subworkflow node %s: workflow_id required", n.ID)
	}
	in := last
	if n.Prompt != "" {
		in = expandTemplateVars(n.Prompt, input, last, rc.Vars)
	}
	return rc.RunSubworkflow(ctx, wid, in)
}

// BashNodeExecutor executes a shell command through the workflow tool runner.
type BashNodeExecutor struct{}

func (e *BashNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	if rc.ToolRunner == nil {
		return "", fmt.Errorf("bash node %s: tool runner unavailable", n.ID)
	}
	script := n.Prompt
	if n.Config != nil {
		if s, ok := n.Config["script"].(string); ok && s != "" {
			script = s
		}
	}
	script = expandTemplateVars(script, input, last, rc.Vars)

	// Route shell execution through the same governed tool path as normal tool nodes.
	argsJSON := fmt.Sprintf(`{"command": %q}`, script)
	return rc.InvokeTool(ctx, "execute_bash", argsJSON)
}

// DatabaseNodeExecutor executes a SQLite/SQL query through the workflow tool runner.
type DatabaseNodeExecutor struct{}

func (e *DatabaseNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	if rc.ToolRunner == nil {
		return "", fmt.Errorf("database node %s: tool runner unavailable", n.ID)
	}
	query := n.Prompt
	if n.Config != nil {
		if q, ok := n.Config["query"].(string); ok && q != "" {
			query = q
		}
	}
	query = expandTemplateVars(query, input, last, rc.Vars)

	argsJSON := fmt.Sprintf(`{"query": %q}`, query)
	return rc.InvokeTool(ctx, "sqlite_query", argsJSON)
}
