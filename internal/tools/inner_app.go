package tools



import (

	"context"

	"encoding/json"

	"strings"



	"github.com/cloudwego/eino/components/tool/utils"



	"agentgo/internal/apps"

)



// InnerAppInvoker is implemented by bridge.Runtime (unified invoke path).

type InnerAppInvoker interface {

	InvokeInnerApp(ctx context.Context, name, input, capability, action, payloadJSON string) apps.InvokeResult

}



type registerInnerAppIn struct {

	Name         string `json:"name"`

	Description  string `json:"description"`

	Kind         string `json:"kind" jsonschema:"description=workflow, agent, or ui"`

	WorkflowID   string `json:"workflow_id,omitempty"`

	SystemPrompt string `json:"system_prompt,omitempty"`

	BundlePath   string `json:"bundle_path,omitempty"`

	Exports      string `json:"exports,omitempty" jsonschema:"description=comma-separated capability names"`

}



type registerInnerAppOut struct {

	ID      string `json:"id"`

	Message string `json:"message"`

}



type invokeInnerAppIn struct {

	AppName    string `json:"app_name"`

	Input      string `json:"input"`

	Capability string `json:"capability,omitempty"`

}



type invokeInnerAppOut struct {

	Output string `json:"output"`

	Error  string `json:"error,omitempty"`

}



type invokeAppCapabilityIn struct {

	AppName     string `json:"app_name"`

	Capability  string `json:"capability"`

	PayloadJSON string `json:"payload_json,omitempty"`

}



// RegisterInnerAppTools wires PyBot-style inner apps (L5) as invokable tools + UI/gateway parity.

func RegisterInnerAppTools(r *Registry, store *apps.Store, inv InnerAppInvoker, onRegistered func(apps.InnerApp)) error {

	if store == nil {

		return nil

	}

	reg, err := utils.InferTool("register_inner_app",

		"Register a system-integrated inner app (workflow, agent, or ui bundle) callable via invoke_inner_app and desktop UI.",

		func(_ context.Context, in registerInnerAppIn) (registerInnerAppOut, error) {

			name := strings.TrimSpace(in.Name)

			if name == "" {

				return registerInnerAppOut{Message: "name required"}, nil

			}

			kind := strings.ToLower(strings.TrimSpace(in.Kind))

			if kind != "workflow" && kind != "agent" && kind != "ui" {

				return registerInnerAppOut{Message: "kind must be workflow, agent, or ui"}, nil

			}

			if kind == "workflow" && strings.TrimSpace(in.WorkflowID) == "" {

				return registerInnerAppOut{Message: "workflow_id required for workflow kind"}, nil

			}

			if kind == "agent" && strings.TrimSpace(in.SystemPrompt) == "" {

				return registerInnerAppOut{Message: "system_prompt required for agent kind"}, nil

			}

			bp := strings.TrimSpace(in.BundlePath)

			if kind == "ui" && bp == "" {

				bp = name

			}

			a := apps.InnerApp{

				Name: name, Description: in.Description, Kind: kind,

				WorkflowID: in.WorkflowID, SystemPrompt: in.SystemPrompt,

				BundlePath: bp, Exports: apps.ParseExports(in.Exports),

				Enabled: true,

			}

			if err := store.Upsert(context.Background(), a); err != nil {

				return registerInnerAppOut{Message: err.Error()}, nil

			}

			got, _ := store.GetByName(context.Background(), name)

			if onRegistered != nil {

				onRegistered(got)

			}

			return registerInnerAppOut{ID: got.ID, Message: "inner app registered; UI: 应用 panel; agents: invoke_inner_app"}, nil

		})

	if err != nil {

		return err

	}

	r.AddTool(reg)



	list, err := utils.InferTool("list_inner_apps",

		"List registered inner apps (workflow/agent/ui) available as tools and desktop apps.",

		func(_ context.Context, _ struct{}) (string, error) {

			rows, err := store.List(context.Background(), 50)

			if err != nil {

				return "", err

			}

			b, _ := json.Marshal(rows)

			return string(b), nil

		})

	if err != nil {

		return err

	}

	r.AddTool(list)



	if inv == nil {

		return nil

	}



	invTool, err := utils.InferTool("invoke_inner_app",

		"Invoke a registered inner app by name (workflow, agent, or ui API action).",

		func(ctx context.Context, in invokeInnerAppIn) (invokeInnerAppOut, error) {

			name := strings.TrimSpace(in.AppName)

			if name == "" {

				return invokeInnerAppOut{Error: "app_name required"}, nil

			}

			res := inv.InvokeInnerApp(ctx, name, in.Input, in.Capability, "", "")

			if res.Error != "" {

				return invokeInnerAppOut{Error: res.Error}, nil

			}

			return invokeInnerAppOut{Output: res.Output}, nil

		})

	if err != nil {

		return err

	}

	r.AddTool(invTool)



	capTool, err := utils.InferTool("invoke_app_capability",

		"Invoke an exported capability of another inner app (PyBot App Matrix style).",

		func(ctx context.Context, in invokeAppCapabilityIn) (invokeInnerAppOut, error) {

			name := strings.TrimSpace(in.AppName)

			cap := strings.TrimSpace(in.Capability)

			if name == "" || cap == "" {

				return invokeInnerAppOut{Error: "app_name and capability required"}, nil

			}

			res := inv.InvokeInnerApp(ctx, name, "", cap, cap, in.PayloadJSON)

			if res.Error != "" {

				return invokeInnerAppOut{Error: res.Error}, nil

			}

			return invokeInnerAppOut{Output: res.Output}, nil

		})

	if err != nil {

		return err

	}

	r.AddTool(capTool)

	return nil

}


