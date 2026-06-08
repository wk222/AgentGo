package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// ToolRunner is the single workflow tool invocation entrypoint.
type ToolRunner interface {
	Invoke(ctx context.Context, toolName, argsJSON string) (string, error)
}

// ToolRunnerFunc adapts a function into a ToolRunner.
type ToolRunnerFunc func(ctx context.Context, toolName, argsJSON string) (string, error)

func (f ToolRunnerFunc) Invoke(ctx context.Context, toolName, argsJSON string) (string, error) {
	return f(ctx, toolName, argsJSON)
}

// ToolsBridge runs workflow tool/bash/database nodes through compose.ToolsNode (trace + ToolCallMiddlewares).
type ToolsBridge struct {
	node      *compose.ToolsNode
	lookup    func(name string) (einotool.BaseTool, bool)
	callbacks []callbacks.Handler
}

// ToolsBridgeConfig builds a ToolsBridge.
type ToolsBridgeConfig struct {
	Tools               []einotool.BaseTool
	ToolCallMiddlewares []compose.ToolMiddleware
	Callbacks           []callbacks.Handler
	// Lookup resolves a tool by name for per-call WithToolList; defaults to indexing Tools.
	Lookup func(name string) (einotool.BaseTool, bool)
}

// NewToolsBridge creates an Eino ToolsNode with the given tools and middleware chain.
func NewToolsBridge(ctx context.Context, cfg ToolsBridgeConfig) (*ToolsBridge, error) {
	tn, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{
		Tools:               cfg.Tools,
		ToolCallMiddlewares: cfg.ToolCallMiddlewares,
	})
	if err != nil {
		return nil, fmt.Errorf("workflow tools node: %w", err)
	}
	lookup := cfg.Lookup
	if lookup == nil {
		byName := make(map[string]einotool.BaseTool)
		for _, t := range cfg.Tools {
			if t == nil {
				continue
			}
			info, err := t.Info(ctx)
			if err != nil || info == nil || strings.TrimSpace(info.Name) == "" {
				continue
			}
			byName[info.Name] = t
		}
		lookup = func(name string) (einotool.BaseTool, bool) {
			t, ok := byName[strings.TrimSpace(name)]
			return t, ok
		}
	}
	return &ToolsBridge{node: tn, lookup: lookup, callbacks: cfg.Callbacks}, nil
}

var workflowToolCallSeq uint64

// Invoke executes one tool call via ToolsNode (governance/heal middlewares apply when configured).
func (b *ToolsBridge) Invoke(ctx context.Context, toolName, argsJSON string) (string, error) {
	if b == nil || b.node == nil {
		return "", fmt.Errorf("workflow: tools bridge unavailable")
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return "", fmt.Errorf("workflow: tool name required")
	}
	bt, ok := b.lookup(toolName)
	if !ok || bt == nil {
		return "", fmt.Errorf("tool %q not found", toolName)
	}
	argsJSON = strings.TrimSpace(argsJSON)
	if argsJSON == "" {
		argsJSON = "{}"
	}
	if len(b.callbacks) > 0 {
		ctx = callbacks.InitCallbacks(ctx, &callbacks.RunInfo{
			Name:      "workflow_tool",
			Type:      "ToolsNode",
			Component: components.ComponentOfTool,
		}, b.callbacks...)
	}
	callID := fmt.Sprintf("wf_%d", atomic.AddUint64(&workflowToolCallSeq, 1))
	msg := schema.AssistantMessage("", []schema.ToolCall{{
		ID: callID,
		Function: schema.FunctionCall{
			Name:      toolName,
			Arguments: argsJSON,
		},
	}})
	outs, err := b.node.Invoke(ctx, msg, compose.WithToolList(bt))
	if err != nil {
		return "", err
	}
	return toolMessagesResult(outs)
}

func toolMessagesResult(outs []*schema.Message) (string, error) {
	if len(outs) == 0 {
		return "", fmt.Errorf("workflow: empty tool output")
	}
	for _, m := range outs {
		if m != nil && m.Role == schema.Tool {
			return m.Content, nil
		}
	}
	if outs[0] != nil {
		return outs[0].Content, nil
	}
	return "", fmt.Errorf("workflow: nil tool message")
}

// InvokeTool executes workflow tools through the configured ToolRunner.
func (rc *RunContext) InvokeTool(ctx context.Context, toolName, argsJSON string) (string, error) {
	if rc != nil && rc.ToolRunner != nil {
		return rc.ToolRunner.Invoke(ctx, toolName, argsJSON)
	}
	return "", fmt.Errorf("workflow: no tool runner")
}
