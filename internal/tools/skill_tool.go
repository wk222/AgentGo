package tools

import (
	"context"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/skills"
)

type activateSkillInput struct {
	SkillID string `json:"skill_id" jsonschema:"description=Skill id or name\\, e.g. skill:office-pptx. An unknown id returns the available ones."`
}

type activateSkillOutput struct {
	Message   string   `json:"message"`
	Content   string   `json:"content,omitempty"`
	Available []string `json:"available,omitempty"`
	// Workspace skills only: where the skill lives, what ships with it, and whether its scripts can run here.
	SkillDir  string            `json:"skill_dir,omitempty"`
	Resources []string          `json:"resources,omitempty"`
	Readiness *skills.Readiness `json:"readiness,omitempty"`
	Hint      string            `json:"hint,omitempty"`
}

// RegisterActivateSkill adds activate_skill tool.
func RegisterActivateSkill(r *Registry, loader *skills.Loader) error {
	if err := registerActivateSkill(r, loader); err != nil {
		return err
	}
	if loader == nil {
		return nil
	}
	return registerRunSkillScript(r, loader)
}

// skillIDs lists the ids the loader knows, sorted.
func skillIDs(loader *skills.Loader) []string {
	list := loader.List()
	ids := make([]string, 0, len(list))
	for _, s := range list {
		ids = append(ids, s.ID)
	}
	sort.Strings(ids)
	return ids
}

func registerActivateSkill(r *Registry, loader *skills.Loader) error {
	if loader == nil {
		return nil
	}
	desc := "Load a skill (a workspace SKILL.md or one built into AgentGo) and return its full instructions, which you then follow for the current task. " +
		"Load one before building Word, Excel or PowerPoint files: skill:office is the entry, with skill:office-xlsx, skill:office-pptx and skill:office-docx for details."
	loader.Reload()
	if ids := skillIDs(loader); len(ids) > 0 {
		shown := ids
		if len(shown) > 16 {
			shown = shown[:16]
		}
		desc += " Available now: " + strings.Join(shown, ", ") + "."
	}
	t, err := utils.InferTool("activate_skill", desc,
		func(_ context.Context, in activateSkillInput) (activateSkillOutput, error) {
			id := strings.TrimSpace(in.SkillID)
			find := func() (skills.Skill, bool) {
				if sk, ok := loader.Get(id); ok {
					return sk, true
				}
				return loader.Get("skill:" + strings.TrimPrefix(id, "skill:"))
			}
			sk, ok := find()
			if !ok {
				loader.Reload()
				sk, ok = find()
			}
			if !ok {
				return activateSkillOutput{Message: "skill not found: " + id, Available: skillIDs(loader)}, nil
			}
			content := skills.ClipUTF8(sk.Content, skills.MaxContextBytes)
			if len(content) < len(sk.Content) {
				content += "\n…(truncated)"
			}
			out := activateSkillOutput{
				Message: "skill loaded: " + sk.Name + " — " + sk.Description,
				Content: content,
			}
			if sk.Dir != "" {
				out.SkillDir = sk.Dir
				out.Resources = skills.Resources(sk)
				if len(sk.Requires.Bins) > 0 || len(sk.Credentials) > 0 {
					rd := loader.Check(sk)
					out.Readiness = &rd
					if !rd.OK {
						out.Hint = "This skill's scripts cannot run yet: " + describeMissing(rd) +
							". Tell the user what to install or set; do not look for the values yourself."
					}
				}
				if out.Hint == "" && hasScript(out.Resources) {
					out.Hint = "Run the skill's scripts with run_skill_script(skill_id, script, args). It checks the requirements and passes the skill's credentials to the script without showing them to you."
				}
			}
			return out, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
