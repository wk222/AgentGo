package tools

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/google/uuid"

	"agentgo/internal/interactive"
)

type askUserInput struct {
	Prompt   string `json:"prompt" jsonschema:"description=Question for the user"`
	Choices  string `json:"choices" jsonschema:"description=JSON array of {id,label} options"`
	Multiple bool   `json:"multiple"`
	FreeText bool   `json:"free_text"`
}

type askUserOutput struct {
	Answer string `json:"answer"`
}

func registerAskUser(r *Registry) error {
	t, err := utils.InferTool("ask_user",
		"Ask the user a question. Supports single/multi choice and free text. Pauses until the user answers in the UI.",
		func(ctx context.Context, in askUserInput) (askUserOutput, error) {
			wasInterrupted, _, _ := tool.GetInterruptState[any](ctx)
			if !wasInterrupted {
				qid := uuid.NewString()
				var choices []interactive.Choice
				if in.Choices != "" {
					_ = json.Unmarshal([]byte(in.Choices), &choices)
				}
				payload := interactive.QuestionPayload{
					Tool: "ask_user", Prompt: in.Prompt, Choices: choices,
					Multiple: in.Multiple, FreeText: in.FreeText, QuestionID: qid,
				}
				interactive.Register(payload)
				return askUserOutput{}, tool.Interrupt(ctx, payload)
			}
			isResume, hasData, ans := tool.GetResumeContext[string](ctx)
			if isResume && hasData {
				return askUserOutput{Answer: ans}, nil
			}
			return askUserOutput{Answer: "{}"}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
