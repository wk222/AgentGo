package tools

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/skills"
)

type activateSkillInput struct {
	SkillID string `json:"skill_id" jsonschema:"description=Skill id from ListSkills, e.g. skill:code_review"`
}

type activateSkillOutput struct {
	Message string `json:"message"`
}

// RegisterActivateSkill adds activate_skill tool.
func RegisterActivateSkill(r *Registry, loader *skills.Loader) error {
	return registerActivateSkill(r, loader)
}

func registerActivateSkill(r *Registry, loader *skills.Loader) error {
	if loader == nil {
		return nil
	}
	t, err := utils.InferTool("activate_skill",
		"Load a workspace SKILL.md into context for the current task (dynamic skill injection).",
		func(_ context.Context, in activateSkillInput) (activateSkillOutput, error) {
			id := strings.TrimSpace(in.SkillID)
			sk, ok := loader.Get(id)
			if !ok {
				loader.Reload()
				sk, ok = loader.Get(id)
			}
			if !ok {
				return activateSkillOutput{Message: "skill not found: " + id}, nil
			}
			return activateSkillOutput{Message: "skill loaded: " + sk.Name + " — " + sk.Description}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
