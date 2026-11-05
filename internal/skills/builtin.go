package skills

import (
	"embed"
	"io/fs"
	"path"
	"sort"
	"strings"

	"agentgo/internal/config"
)

// ScopeBuiltin marks a skill that ships inside the program. A skill with the
// same name in the workspace replaces it, so a team can adapt the wording.
const ScopeBuiltin = "builtin"

// Built-in skills are plain SKILL.md files compiled into the binary, so they
// are there on a PC that cannot download anything.
//
//go:embed builtin/*/SKILL.md
var builtinFS embed.FS

// builtinGates ties a group of built-in skills to the switch of the tools they
// describe: a skill that explains tools which are turned off would only mislead.
var builtinGates = []struct {
	prefix string
	on     func() bool
}{
	{"office", config.OfficeDoc.On},
	{"document-qa", config.DocIndex.On},
}

func builtinEnabled(name string) bool {
	for _, g := range builtinGates {
		if strings.HasPrefix(name, g.prefix) && !g.on() {
			return false
		}
	}
	return true
}

// builtinSkills returns the skills compiled into the binary, ordered by name.
func builtinSkills() []Skill {
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() || !builtinEnabled(e.Name()) {
			continue
		}
		b, err := fs.ReadFile(builtinFS, path.Join("builtin", e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		content := strings.ReplaceAll(string(b), "\r\n", "\n")
		out = append(out, Skill{
			ID:          "skill:" + e.Name(),
			Name:        e.Name(),
			Path:        "builtin:" + e.Name() + "/SKILL.md",
			Description: firstParagraph(content),
			Scope:       ScopeBuiltin,
			Content:     content,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
