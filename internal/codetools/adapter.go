// Package codetools adapts Crush's code-aware tools (LSP diagnostics, view,
// edit, grep, ...) to Eino tools, so AgentGo's own agent loop can use them.
// The tool implementations live in the Crush fork (pkg/codetools).
package codetools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ct "github.com/charmbracelet/crush/pkg/codetools"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"agentgo/internal/governance"
)

// ToolName maps a Crush tool name into AgentGo's namespace ("view" -> "code_view";
// "lsp_*" tools keep their name).
func ToolName(crushName string) string {
	if strings.HasPrefix(crushName, "lsp_") {
		return crushName
	}
	return "code_" + crushName
}

// IsMutating reports whether a Crush tool changes files.
func IsMutating(crushName string) bool {
	switch crushName {
	case "edit", "multiedit", "write", "lsp_rename", "lsp_replace_symbol":
		return true
	}
	return false
}

// Source names this package's tools in governance audit records.
const Source = "codetools"

// RegisterTraits declares each tool's traits to governance, so the policy
// learns which tools change files from IsMutating alone instead of a second
// list of names. The returned func withdraws them all.
func RegisterTraits(tools []ct.Tool) (dispose func()) {
	disposers := make([]func(), 0, len(tools))
	for _, t := range tools {
		disposers = append(disposers, governance.RegisterToolTraits(ToolName(t.Name),
			governance.ToolTraits{Source: Source, Mutating: IsMutating(t.Name)}))
	}
	return func() {
		for _, d := range disposers {
			d()
		}
	}
}

// AlwaysVisible reports which Crush tools the model should see without
// tool_search: the core read / diagnose / edit loop. The rest (references,
// symbols, definition, call hierarchy, ls) stay discoverable on demand.
func AlwaysVisible(crushName string) bool {
	switch crushName {
	case "view", "grep", "glob", "lsp_diagnostics", "edit", "multiedit", "write":
		return true
	}
	return false
}

type einoAdapter struct {
	name string
	tool ct.Tool
	info *schema.ToolInfo
}

// Adapt converts Crush tools into Eino invokable tools.
func Adapt(tools []ct.Tool) ([]einotool.BaseTool, error) {
	out := make([]einotool.BaseTool, 0, len(tools))
	for _, t := range tools {
		info, err := toolInfo(t)
		if err != nil {
			return nil, fmt.Errorf("codetools: tool %q: %w", t.Name, err)
		}
		out = append(out, &einoAdapter{name: info.Name, tool: t, info: info})
	}
	return out, nil
}

func toolInfo(t ct.Tool) (*schema.ToolInfo, error) {
	raw, err := json.Marshal(t.Schema)
	if err != nil {
		return nil, err
	}
	var js jsonschema.Schema
	if err := json.Unmarshal(raw, &js); err != nil {
		return nil, err
	}
	return &schema.ToolInfo{
		Name:        ToolName(t.Name),
		Desc:        t.Description,
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&js),
	}, nil
}

func (a *einoAdapter) Info(context.Context) (*schema.ToolInfo, error) { return a.info, nil }

// InvokableRun runs the Crush tool. Tool-level failures are returned as text so
// the model can react to them (an error return would abort the agent turn).
func (a *einoAdapter) InvokableRun(ctx context.Context, argsJSON string, _ ...einotool.Option) (string, error) {
	callID := compose.GetToolCallID(ctx)
	res, err := a.tool.Run(ctx, governance.SessionIDFromContext(ctx), callID, argsJSON)
	if err != nil {
		return "", fmt.Errorf("%s: %w", a.name, err)
	}
	if res.IsError {
		return "ERROR: " + res.Content, nil
	}
	if strings.TrimSpace(res.Content) == "" && a.tool.Name == "lsp_diagnostics" {
		return emptyDiagnostics, nil
	}
	return nonEmptyResult(res.Content), nil
}

// emptyDiagnostics replaces Crush's empty diagnostics output. Empty is ambiguous
// upstream: it means "clean" but also "no language server is active for this
// file", and the model must not treat the latter as proof the code compiles.
const emptyDiagnostics = "No diagnostics reported by any active language server. " +
	"The code is clean OR no language server handles this file type / has finished analysing yet; " +
	"if correctness matters, confirm with the project's build or test command."

// nonEmptyResult guarantees a tool response is never empty: an empty tool
// message gets dropped downstream, leaving the assistant's tool_call without a
// response and making the next model request fail with HTTP 400.
func nonEmptyResult(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no output)"
	}
	return s
}
