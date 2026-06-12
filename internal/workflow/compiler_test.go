package workflow

import (
	"context"
	"strings"
	"testing"
)

func TestCompileVueFlow_LLMChain(t *testing.T) {
	ctx := context.Background()

	// 1. Define a VueFlow JSON for a Start -> LLM -> End graph
	vueFlowJSON := `{
		"nodes": [
			{
				"id": "start",
				"type": "start",
				"position": {"x": 100, "y": 100}
			},
			{
				"id": "translator",
				"type": "llm",
				"position": {"x": 300, "y": 100},
				"data": {
					"prompt": "Translate: {{input}}"
				}
			},
			{
				"id": "end",
				"type": "end",
				"position": {"x": 500, "y": 100}
			}
		],
		"edges": [
			{
				"id": "e1",
				"source": "start",
				"target": "translator"
			},
			{
				"id": "e2",
				"source": "translator",
				"target": "end"
			}
		]
	}`

	// 2. Mock LLM generator inside RunContext
	rc := &RunContext{
		Input: "Hello World",
		LLMGenerate: func(ctx context.Context, prompt string) (string, error) {
			if !strings.Contains(prompt, "Hello World") {
				t.Errorf("expected prompt to contain Hello World, got: %s", prompt)
			}
			return "Bonjour le monde", nil
		},
	}

	// 3. Compile the graph
	runnable, err := CompileVueFlow(ctx, vueFlowJSON, rc)
	if err != nil {
		t.Fatalf("failed to compile VueFlow: %v", err)
	}

	// 4. Run the compiled graph
	initialState := &WorkflowState{
		OriginalInput: rc.Input,
		LastOutput:    "",
		Vars:          make(map[string]string),
	}

	finalState, err := runnable.Invoke(ctx, initialState)
	if err != nil {
		t.Fatalf("failed to execute compiled graph: %v", err)
	}

	if finalState.LastOutput != "Bonjour le monde" {
		t.Fatalf("expected 'Bonjour le monde', got: %s", finalState.LastOutput)
	}
}

func TestCompileVueFlow_ToolNode(t *testing.T) {
	ctx := context.Background()

	// 1. Define a VueFlow JSON for a Start -> Tool -> End graph
	vueFlowJSON := `{
		"nodes": [
			{
				"id": "start",
				"type": "start",
				"position": {"x": 100, "y": 100}
			},
			{
				"id": "tool_node",
				"type": "tool",
				"position": {"x": 300, "y": 100},
				"data": {
					"tool_name": "calc_tool",
					"args": "{\"x\": 5}"
				}
			},
			{
				"id": "end",
				"type": "end",
				"position": {"x": 500, "y": 100}
			}
		],
		"edges": [
			{
				"id": "e1",
				"source": "start",
				"target": "tool_node"
			},
			{
				"id": "e2",
				"source": "tool_node",
				"target": "end"
			}
		]
	}`

	// 2. Mock ToolRunner inside RunContext
	rc := &RunContext{
		Input: "original input",
		ToolRunner: ToolRunnerFunc(func(ctx context.Context, toolName string, argsJSON string) (string, error) {
			if toolName != "calc_tool" {
				t.Errorf("expected toolName to be calc_tool, got: %s", toolName)
			}
			if !strings.Contains(argsJSON, "5") {
				t.Errorf("expected args to contain 5, got: %s", argsJSON)
			}
			return "result: 10", nil
		}),
	}

	// 3. Compile the graph
	runnable, err := CompileVueFlow(ctx, vueFlowJSON, rc)
	if err != nil {
		t.Fatalf("failed to compile VueFlow: %v", err)
	}

	// 4. Run the compiled graph
	initialState := &WorkflowState{
		OriginalInput: rc.Input,
		LastOutput:    "",
		Vars:          make(map[string]string),
	}

	finalState, err := runnable.Invoke(ctx, initialState)
	if err != nil {
		t.Fatalf("failed to execute compiled graph: %v", err)
	}

	if finalState.LastOutput != "result: 10" {
		t.Fatalf("expected 'result: 10', got: %s", finalState.LastOutput)
	}
}

func TestCompileVueFlow_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	_, err := CompileVueFlow(ctx, "{invalid json}", &RunContext{})
	if err == nil {
		t.Fatal("expected error when compiling invalid JSON")
	}
}
