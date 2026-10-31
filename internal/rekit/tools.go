package rekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"agentgo/internal/governance"

	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// Source names this package's tools in governance and tool catalog records.
const Source = "rekit"

// Mutating tools write decoded copies, append to the case ledger, render REPORT.md,
// or launch an allowlisted external binary with output spilled under <home>/cases/<case>/spill.
var mutatingTools = map[string]bool{
	"re_xor_probe":    true,
	"re_evidence_add": true,
	"re_report":       true,
	"re_tool_run":     true,
	"re_gate":         true,
}

// IsMutating reports whether a rekit tool can write files or launch an external process.
// Read-only canvases (CanvasFocused) hide mutating tools while keeping static inspection tools.
func IsMutating(name string) bool {
	return mutatingTools[name]
}

// Risk returns the governance risk level of a rekit tool.
func Risk(name string) governance.RiskLevel {
	if mutatingTools[name] {
		return governance.RiskMedium
	}
	return governance.RiskLow
}

// Tools builds the 11 re_* Eino tools backed by an in-process MCP server with a fixed home dir.
func Tools(ctx context.Context, home string, allowDynamic bool) ([]einotool.BaseTool, func(), error) {
	return ToolsWithHome(ctx, func() string { return home }, allowDynamic)
}

// ToolsWithHome builds the 11 re_* Eino tools backed by an in-process MCP server whose
// default case directory dynamically follows homeFn() (e.g. the active workspace).
func ToolsWithHome(ctx context.Context, homeFn func() string, allowDynamic bool) ([]einotool.BaseTool, func(), error) {
	srv := NewServerWithHome(homeFn, allowDynamic)
	cli, err := client.NewInProcessClient(srv)
	if err != nil {
		return nil, nil, fmt.Errorf("rekit in-process client: %w", err)
	}
	if err := cli.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("rekit start client: %w", err)
	}
	var req mcp.InitializeRequest
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "agentgo-rekit", Version: Version}
	if _, err := cli.Initialize(ctx, req); err != nil {
		_ = cli.Close()
		return nil, nil, fmt.Errorf("rekit init client: %w", err)
	}
	rawList, err := einomcp.GetTools(ctx, &einomcp.Config{Cli: cli})
	if err != nil {
		_ = cli.Close()
		return nil, nil, fmt.Errorf("rekit get tools: %w", err)
	}
	list := make([]einotool.BaseTool, 0, len(rawList))
	for _, bt := range rawList {
		if inv, ok := bt.(einotool.InvokableTool); ok {
			list = append(list, plainTextMCPTool{inner: inv})
		} else {
			list = append(list, bt)
		}
	}
	closeFn := func() { _ = cli.Close() }
	return list, closeFn, nil
}

// plainTextMCPTool unwraps einomcp's {"content":[{"type":"text","text":"..."}]} envelope
// so the model receives the clean Reply JSON directly without double-escaping.
type plainTextMCPTool struct {
	inner einotool.InvokableTool
}

func (t plainTextMCPTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.inner.Info(ctx)
}

func (t plainTextMCPTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
	out, err := t.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		return out, err
	}
	var env struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if json.Unmarshal([]byte(out), &env) == nil && len(env.Content) == 1 && env.Content[0].Type == "text" {
		if env.IsError {
			return "", errors.New(env.Content[0].Text)
		}
		return env.Content[0].Text, nil
	}
	return out, nil
}
