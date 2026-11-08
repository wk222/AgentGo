package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// NodeResult is what one run of a custom node produced.
type NodeResult struct {
	Raw     string         // what the backend returned (after unwrapping tool envelopes)
	Outputs map[string]any // declared outputs, typed (key "result" when none are declared)
	Text    string         // what the node hands to the next one
}

// CustomNodeExecutor runs workflow nodes of type "custom" and "custom:<id>".
//
// The node binds each declared input in this order: config.inputs[NAME]
// (templates allowed), a key NAME in the previous node's JSON output, the
// previous output itself when the node has exactly one input, then the
// declared default. It runs the backend, checks the result against the
// declared outputs, writes Vars "<nodeID>.<output>" and passes on a JSON
// object of the outputs (or the backend text when none are declared).
type CustomNodeExecutor struct{}

func (e *CustomNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	id := CustomNodeID(n)
	if id == "" {
		return "", fmt.Errorf("custom node %s: no node id (use type custom:<id> or config.node_id)", n.ID)
	}
	if rc.ResolveNode == nil {
		return "", fmt.Errorf("custom node %s: custom nodes are not available in this run", n.ID)
	}
	m, ok := rc.ResolveNode(id)
	if !ok {
		return "", fmt.Errorf("custom node %s: node %q is not defined", n.ID, id)
	}
	if m.Retired {
		return "", fmt.Errorf("custom node %s: node %q is retired", n.ID, id)
	}
	env := nodeEnv{Input: input, Last: last, Vars: rc.Vars}
	supplied, err := suppliedInputs(n, env)
	if err != nil {
		return "", fmt.Errorf("custom node %s (%s): %w", n.ID, id, err)
	}
	inputs, err := bindInputs(m, supplied, last, true)
	if err != nil {
		return "", fmt.Errorf("custom node %s (%s): %w", n.ID, id, err)
	}
	res, err := runNode(ctx, m, inputs, rc, env)
	if err != nil {
		return "", fmt.Errorf("custom node %s (%s): %w", n.ID, id, err)
	}
	if rc.Vars != nil {
		for k, v := range res.Outputs {
			rc.Vars[n.ID+"."+k] = stringifyValue(v)
		}
	}
	return res.Text, nil
}

// RunNode runs one custom node on already-bound inputs, outside any workflow.
func RunNode(ctx context.Context, m NodeManifest, inputs map[string]any, rc RunContext) (NodeResult, error) {
	return runNode(ctx, m, inputs, rc, nodeEnv{Vars: rc.Vars})
}

// suppliedInputs reads the node's config.inputs (an object, or JSON text as the
// canvas stores it) and expands workflow templates in it.
func suppliedInputs(n Node, env nodeEnv) (map[string]any, error) {
	var raw any
	if n.Config != nil {
		raw = n.Config["inputs"]
	}
	var obj map[string]any
	switch x := raw.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		obj = x
	case string:
		if strings.TrimSpace(x) == "" {
			return nil, nil
		}
		if err := json.Unmarshal([]byte(x), &obj); err != nil {
			return nil, fmt.Errorf("config.inputs is not a JSON object: %v", err)
		}
	default:
		return nil, fmt.Errorf("config.inputs must be an object, got %s", valueKind(raw))
	}
	expanded, _ := expandAny(obj, nil, env).(map[string]any)
	return expanded, nil
}

func fieldNames(fs []NodeField) string {
	names := make([]string, 0, len(fs))
	for _, f := range fs {
		names = append(names, f.Name)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func parseObject(s string) map[string]any {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") {
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(s), &m) != nil {
		return nil
	}
	return m
}

// bindInputs resolves every declared input of m from the explicitly supplied
// values and, when fallback is set, from the previous node's output.
func bindInputs(m NodeManifest, supplied map[string]any, last string, fallback bool) (map[string]any, error) {
	known := map[string]bool{}
	for _, f := range m.Inputs {
		known[f.Name] = true
	}
	for k := range supplied {
		if !known[k] {
			return nil, fmt.Errorf("unknown input %q (declared: %s)", k, fieldNames(m.Inputs))
		}
	}
	var lastObj map[string]any
	if fallback {
		lastObj = parseObject(last)
	}
	out := make(map[string]any, len(m.Inputs))
	var problems []string
	for _, f := range m.Inputs {
		raw, found := supplied[f.Name]
		if !found && fallback {
			raw, found = lastObj[f.Name]
		}
		if !found && fallback && len(m.Inputs) == 1 && strings.TrimSpace(last) != "" {
			raw, found = last, true
		}
		if !found {
			switch {
			case f.Default != nil:
				out[f.Name] = f.Default
			case f.Optional:
			default:
				problems = append(problems, fmt.Sprintf("input %q is required", f.Name))
			}
			continue
		}
		v, err := coerceValue(f.Type, raw)
		if err != nil {
			problems = append(problems, fmt.Sprintf("input %q: %v", f.Name, err))
			continue
		}
		if len(f.Enum) > 0 {
			s, _ := v.(string)
			allowed := false
			for _, e := range f.Enum {
				allowed = allowed || e == s
			}
			if !allowed {
				problems = append(problems, fmt.Sprintf("input %q must be one of %s, got %q", f.Name, strings.Join(f.Enum, ", "), truncateText(s, 60)))
				continue
			}
		}
		out[f.Name] = v
	}
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	return out, nil
}

func runNode(ctx context.Context, m NodeManifest, inputs map[string]any, rc RunContext, env nodeEnv) (NodeResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, m.Timeout())
	defer cancel()
	raw, err := callBackend(callCtx, m, inputs, rc, env)
	if err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return NodeResult{}, fmt.Errorf("timed out after %s", m.Timeout())
		}
		return NodeResult{}, err
	}
	if limit := m.OutputLimit(); len(raw) > limit {
		return NodeResult{Raw: truncateText(raw, 200)}, fmt.Errorf("backend result is %d bytes, over the %d byte limit", len(raw), limit)
	}
	return parseOutputs(m, raw)
}

func callBackend(ctx context.Context, m NodeManifest, inputs map[string]any, rc RunContext, env nodeEnv) (string, error) {
	b := m.Backend
	switch b.Kind {
	case BackendTool, BackendPython:
		var payload any = inputs
		switch {
		case b.Kind == BackendPython:
			// Generated Python tools take their arguments as one JSON text.
			in, _ := json.Marshal(inputs)
			payload = map[string]any{"arguments": string(in)}
		case b.Args != nil:
			payload = expandAny(b.Args, inputs, env)
		}
		args, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("tool arguments: %w", err)
		}
		raw, err := rc.InvokeTool(ctx, b.Ref, string(args))
		if err != nil {
			return "", err
		}
		return unwrapToolResult(b.Ref, raw)
	case BackendSubworkflow:
		if rc.RunSubworkflow == nil {
			return "", fmt.Errorf("subworkflow runner is unavailable")
		}
		var text string
		switch {
		case b.InputTemplate != "":
			text = stringifyValue(expandAny(b.InputTemplate, inputs, env))
		case len(m.Inputs) == 1:
			text = stringifyValue(inputs[m.Inputs[0].Name])
		default:
			in, _ := json.Marshal(inputs)
			text = string(in)
		}
		return rc.RunSubworkflow(ctx, b.Ref, text)
	}
	return "", fmt.Errorf("unknown backend kind %q", b.Kind)
}

// unwrapToolResult strips the envelopes some tools wrap their result in, so a
// node sees the payload rather than the plumbing: generated Python tools
// ({stdout, stderr, exit_code}) and the run_workflow / workflow_* tools
// ({output, error}). A failed script or workflow becomes a node error.
func unwrapToolResult(ref, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return raw, nil
	}
	var py struct {
		Stdout   *string `json:"stdout"`
		Stderr   *string `json:"stderr"`
		ExitCode *int    `json:"exit_code"`
		Error    string  `json:"error"`
	}
	if json.Unmarshal([]byte(trimmed), &py) == nil && py.Stdout != nil && py.Stderr != nil && py.ExitCode != nil {
		if py.Error != "" {
			return "", errors.New(py.Error)
		}
		if *py.ExitCode != 0 {
			return "", fmt.Errorf("script exited with code %d: %s", *py.ExitCode, truncateText(strings.TrimSpace(*py.Stderr), 600))
		}
		return strings.TrimSpace(*py.Stdout), nil
	}
	if ref == "run_workflow" || strings.HasPrefix(ref, "workflow_") || strings.HasPrefix(ref, "graph_workflow_") {
		var wf struct {
			Output *string `json:"output"`
			Error  string  `json:"error"`
		}
		if json.Unmarshal([]byte(trimmed), &wf) == nil && (wf.Output != nil || wf.Error != "") {
			if wf.Error != "" {
				return "", errors.New(wf.Error)
			}
			return *wf.Output, nil
		}
	}
	return raw, nil
}

// parseOutputs checks a backend result against the declared outputs.
func parseOutputs(m NodeManifest, raw string) (NodeResult, error) {
	res := NodeResult{Raw: raw}
	if len(m.Outputs) == 0 {
		res.Outputs = map[string]any{"result": raw}
		res.Text = raw
		return res, nil
	}
	obj, isObj := extractJSONObject(raw)
	if len(m.Outputs) == 1 {
		// A single output may be the whole backend text (or the whole JSON value).
		f := m.Outputs[0]
		if _, has := obj[f.Name]; !isObj || !has {
			var v any = raw
			if f.Type == FieldJSON {
				var decoded any
				if json.Unmarshal([]byte(strings.TrimSpace(raw)), &decoded) == nil {
					v = decoded
				}
			}
			obj = map[string]any{f.Name: v}
		}
	} else if !isObj {
		return res, fmt.Errorf("the backend must return a JSON object with the declared outputs (%s); got %q", fieldNames(m.Outputs), truncateText(strings.TrimSpace(raw), 160))
	}
	out := make(map[string]any, len(m.Outputs))
	var problems []string
	for _, f := range m.Outputs {
		v, ok := obj[f.Name]
		if !ok || v == nil {
			if !f.Optional {
				keys := make([]string, 0, len(obj))
				for k := range obj {
					keys = append(keys, k)
				}
				problems = append(problems, fmt.Sprintf("output %q is missing (the backend returned keys: %s)", f.Name, strings.Join(keys, ", ")))
			}
			continue
		}
		cv, err := coerceValue(f.Type, v)
		if err != nil {
			problems = append(problems, fmt.Sprintf("output %q: %v", f.Name, err))
			continue
		}
		out[f.Name] = cv
	}
	if len(problems) > 0 {
		return res, errors.New(strings.Join(problems, "; "))
	}
	b, err := json.Marshal(out)
	if err != nil {
		return res, err
	}
	res.Outputs, res.Text = out, string(b)
	return res, nil
}
