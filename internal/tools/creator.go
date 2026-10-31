package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"
)

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)

type createToolInput struct {
	ToolName    string `json:"tool_name" jsonschema:"description=snake_case name, e.g. fetch_weather"`
	Description string `json:"description" jsonschema:"description=What the tool does"`
	Parameters  string `json:"parameters" jsonschema:"description=JSON schema string for parameters"`
	Code        string `json:"code" jsonschema:"description=Python script body; executed in a local Python subprocess on each call with the parsed arguments available as the dict variable args"`
	UsageGuide  string `json:"usage_guide" jsonschema:"description=When the agent should call this tool"`
	EnvVars     string `json:"env_vars" jsonschema:"description=Comma-separated names of host environment variables the tool needs, e.g. WEATHER_API_KEY. Tool processes receive a filtered environment: system basics only, secrets are withheld unless declared here"`
}

type createToolOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Name    string `json:"name,omitempty"`
}

func registerCreateTool(r *Registry, store *DynamicStore, capRegister func(kind, name, scope string), onCompiled func(DynamicToolDef) error) error {
	t, err := utils.InferTool("create_tool",
		"Register a dynamic Python tool: validates the code, persists it and hot-loads it as a named tool discoverable via tool_search.",
		func(ctx context.Context, in createToolInput) (createToolOutput, error) {
			name := strings.TrimSpace(strings.ToLower(in.ToolName))
			if !toolNamePattern.MatchString(name) {
				return createToolOutput{Success: false, Message: "invalid tool_name: use snake_case a-z0-9_"}, nil
			}
			if strings.TrimSpace(in.Description) == "" {
				return createToolOutput{Success: false, Message: "description required"}, nil
			}
			envVars, err := ParseEnvVars(in.EnvVars)
			if err != nil {
				return createToolOutput{Success: false, Message: err.Error()}, nil
			}
			def := DynamicToolDef{
				Name:        name,
				Description: in.Description,
				Parameters:  in.Parameters,
				Code:        in.Code,
				UsageGuide:  in.UsageGuide,
				EnvVars:     envVars,
			}
			if store == nil {
				return createToolOutput{Success: false, Message: "dynamic tool store unavailable"}, nil
			}
			// Validate everything knowable BEFORE persisting: a definition that
			// cannot run must never reach the store, or "stored" and "callable"
			// diverge (the failure used to surface only at first invocation).
			if err := ValidateDynamicToolDef(ctx, def); err != nil {
				return createToolOutput{Success: false, Message: "tool rejected, nothing saved: " + err.Error()}, nil
			}
			// Snapshot any previous definition so a failed registration can be
			// rolled back without destroying the earlier working version.
			prev, prevErr := store.Get(name)
			prevExists := prevErr == nil
			if err := store.Save(def); err != nil {
				return createToolOutput{Success: false, Message: err.Error()}, nil
			}
			rollback := func() {
				if prevExists {
					_ = store.Save(prev)
				} else {
					_ = store.Delete(name)
				}
			}
			if onCompiled != nil {
				if err := onCompiled(def); err != nil {
					rollback()
					return createToolOutput{Success: false, Message: "registration failed, nothing saved: " + err.Error()}, nil
				}
			}
			if capRegister != nil {
				capRegister("tool", name, "agent")
			}
			return createToolOutput{
				Success: true,
				Name:    name,
				Message: fmt.Sprintf("tool %q validated (parameters + Python syntax) and registered; model can call %q directly or find it via tool_search. Registration is not a functional test: call the tool once to verify its behavior", name, name),
			}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

type listDynamicToolsOutput struct {
	Tools string `json:"tools"`
}

func registerListDynamicTools(r *Registry, store *DynamicStore) error {
	t, err := utils.InferTool("list_dynamic_tools",
		"List agent-created dynamic tool definitions stored locally.",
		func(_ context.Context, _ struct{}) (listDynamicToolsOutput, error) {
			if store == nil {
				return listDynamicToolsOutput{Tools: "[]"}, nil
			}
			list, err := store.List()
			if err != nil {
				return listDynamicToolsOutput{}, err
			}
			summaries := make([]json.RawMessage, 0, len(list))
			for _, d := range list {
				summaries = append(summaries, json.RawMessage(d.MarshalSummary()))
			}
			b, _ := json.Marshal(summaries)
			return listDynamicToolsOutput{Tools: string(b)}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
