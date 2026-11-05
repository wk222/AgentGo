package tools

import (
	"context"
	"fmt"
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

const (
	// maxOwnSkillsListed bounds how many of the user's own skills the tool description
	// names; an unknown skill_id returns them all.
	maxOwnSkillsListed = 12
	// triggerRunes is how much of a skill's description goes into the tool description.
	triggerRunes = 70
)

// skillTrigger is the part of a skill description that says when to load it. The
// skills that ship with AgentGo write it as "用户要…时先读本技能：…", so the text up to
// that phrase (or the first full stop or colon) is the trigger.
func skillTrigger(desc string) string {
	desc = strings.Join(strings.Fields(desc), " ")
	if i := strings.Index(desc, "时先读本技能"); i > 0 {
		desc = desc[:i]
	} else if i := strings.IndexAny(desc, "：。"); i > 0 {
		desc = desc[:i]
	}
	if r := []rune(desc); len(r) > triggerRunes {
		desc = string(r[:triggerRunes]) + "…"
	}
	return desc
}

// skillCatalog is the sentence of the activate_skill description that tells the
// model which skills exist and when each applies; without the trigger it can only
// guess from the name. The user's own skills come first. The detailed office
// skills are left out: skill:office names them.
func skillCatalog(list []skills.Skill) string {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	var own, shipped []string
	hasOfficeRouter := false
	for _, s := range list {
		if s.Scope == skills.ScopeBuiltin && s.Name == "office" {
			hasOfficeRouter = true
		}
	}
	for _, s := range list {
		entry := s.ID
		if t := skillTrigger(s.Description); t != "" {
			entry += " (" + t + ")"
		}
		switch {
		case s.Scope != skills.ScopeBuiltin:
			own = append(own, entry)
		case hasOfficeRouter && strings.HasPrefix(s.Name, "office-"):
			// reached through skill:office
		default:
			shipped = append(shipped, entry)
		}
	}
	if len(own)+len(shipped) == 0 {
		return ""
	}
	more := ""
	if len(own) > maxOwnSkillsListed {
		more = fmt.Sprintf("; and %d more (an unknown skill_id returns them all)", len(own)-maxOwnSkillsListed)
		own = own[:maxOwnSkillsListed]
	}
	entries := append(append([]string{}, own...), shipped...)
	return " Skills available now, each with the situation it is for: " + strings.Join(entries, "; ") + more + "."
}

func registerActivateSkill(r *Registry, loader *skills.Loader) error {
	if loader == nil {
		return nil
	}
	desc := "Load a skill (a workspace SKILL.md or one built into AgentGo) and return its full instructions, which you then follow for the current task. " +
		"Load one before building Word, Excel or PowerPoint files: start with skill:office, which also routes to the scenario skills (dashboards, financial models, pitch decks, papers); skill:office-xlsx, skill:office-pptx and skill:office-docx hold the file-type details."
	loader.Reload()
	desc += skillCatalog(loader.List())
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
