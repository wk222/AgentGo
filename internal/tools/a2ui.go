package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/tool/utils"
)

type renderUIInput struct {
	Component  string `json:"component" jsonschema:"description=UI component type: form, card, chart, table, progress, metric, timeline, status, code_block, image_gallery, accordion, list, markdown"`
	DataJSON   any    `json:"data_json" jsonschema:"description=JSON payload (object or string) for the component data"`
	InteractID string `json:"interact_id,omitempty" jsonschema:"description=Optional interaction ID. If set, the agent will wait for user interaction before continuing."`
	Surface    string `json:"surface,omitempty" jsonschema:"description=Target rendering surface: chat (default), panel, modal"`
}

type renderUIOutput struct {
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	InteractID string `json:"interact_id,omitempty"`
	UserAction string `json:"user_action,omitempty"`
	UserData   string `json:"user_data,omitempty"`
}

// A2UIRenderEvent contains all fields emitted when the render_ui tool fires.
type A2UIRenderEvent struct {
	Component  string `json:"component"`
	DataJSON   string `json:"data_json"`
	InteractID string `json:"interact_id,omitempty"`
	Surface    string `json:"surface,omitempty"`
}

func RegisterA2UITool(r *Registry, interactStore *InteractionStore, onRender func(ctx context.Context, ev A2UIRenderEvent)) error {
	t, err := utils.InferTool("render_ui",
		"Render a structured UI component to the user instead of raw text. "+
			"Supported components: form, card, chart, table, progress, metric, timeline, status, code_block, image_gallery, accordion, list, markdown. "+
			"Use the 'surface' field to target chat (default), panel, or modal. "+
			"Set 'interact_id' to wait for user interaction (e.g. form submission).",
		func(ctx context.Context, in renderUIInput) (renderUIOutput, error) {
			var dataJSONStr string
			if in.DataJSON != nil {
				switch v := in.DataJSON.(type) {
				case string:
					dataJSONStr = v
				default:
					b, _ := json.Marshal(v)
					dataJSONStr = string(b)
				}
			}
			ev := A2UIRenderEvent{
				Component:  in.Component,
				DataJSON:   dataJSONStr,
				InteractID: in.InteractID,
				Surface:    in.Surface,
			}
			if onRender != nil {
				onRender(ctx, ev)
			}

			// If no interaction requested, return immediately.
			if in.InteractID == "" || interactStore == nil {
				return renderUIOutput{
					Success: true,
					Message: fmt.Sprintf("Rendered %s component to user.", in.Component),
				}, nil
			}

			// Block waiting for user interaction (up to 5 minutes).
			ch := interactStore.Register(in.InteractID)
			select {
			case result, ok := <-ch:
				if !ok {
					return renderUIOutput{
						Success: false,
						Message: "Interaction cancelled.",
					}, nil
				}
				dataStr := ""
				if result.Data != nil {
					dataStr = string(result.Data)
				}
				return renderUIOutput{
					Success:    true,
					Message:    fmt.Sprintf("User interacted with %s component.", in.Component),
					InteractID: in.InteractID,
					UserAction: result.Action,
					UserData:   dataStr,
				}, nil
			case <-time.After(5 * time.Minute):
				interactStore.Cancel(in.InteractID)
				return renderUIOutput{
					Success: false,
					Message: "Interaction timed out after 5 minutes.",
				}, nil
			case <-ctx.Done():
				interactStore.Cancel(in.InteractID)
				return renderUIOutput{
					Success: false,
					Message: "Interaction cancelled: " + ctx.Err().Error(),
				}, nil
			}
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

// MarshalRenderEvent converts an A2UIRenderEvent to a JSON-friendly map.
func MarshalRenderEvent(ev A2UIRenderEvent) map[string]any {
	m := map[string]any{
		"component": ev.Component,
		"data_json": ev.DataJSON,
	}
	if ev.InteractID != "" {
		m["interact_id"] = ev.InteractID
	}
	if ev.Surface != "" {
		m["surface"] = ev.Surface
	}
	return m
}

// MarshalRenderEventJSON converts an A2UIRenderEvent to a JSON byte slice for gateway broadcast.
func MarshalRenderEventJSON(ev A2UIRenderEvent) []byte {
	b, _ := json.Marshal(MarshalRenderEvent(ev))
	return b
}
