package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"agentgo/internal/config"
)

// Scopes a skill reports besides ScopeBuiltin. Skills of other agents report the
// agent's name ("claude", "codex", ...).
const (
	ScopeWorkspace = "workspace"
	ScopeUser      = "user"  // <data dir>/skills
	ScopeExtra     = "extra" // a folder named in AGENTGO_SKILL_DIRS
)

// Source is one more folder skills are read from, besides the workspace's own.
type Source struct {
	Scope string
	Dir   string
}

// agentSkillDirs are where other agents keep the skills they installed for the
// user, relative to the home folder.
var agentSkillDirs = map[string][]string{
	"agents": {".agents", "skills"},
	"claude": {".claude", "skills"},
	"cursor": {".cursor", "skills"},
	"codex":  {".codex", "skills"},
	"gemini": {".gemini", "skills"},
}

// agentOrder is the precedence "all" uses when two agents hold a skill of the same name.
var agentOrder = []string{"agents", "claude", "cursor", "codex", "gemini"}

// wantedAgents reads AGENTGO_EXTERNAL_SKILLS: "all", or names in the order they
// should win. Unknown names are ignored.
func wantedAgents(v string) []string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "":
		return nil
	case "all", "1", "true", "on", "yes":
		return agentOrder
	}
	var out []string
	seen := map[string]bool{}
	for _, name := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if _, known := agentSkillDirs[name]; known && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// DefaultSources lists the folders besides the workspace's own: <dataDir>/skills
// always, then AGENTGO_SKILL_DIRS, then the other agents named in
// AGENTGO_EXTERNAL_SKILLS (off unless set: those folders can hold dozens of
// skills the user never meant for this app).
func DefaultSources(dataDir string) []Source {
	var out []Source
	if dataDir != "" {
		out = append(out, Source{Scope: ScopeUser, Dir: filepath.Join(dataDir, "skills")})
	}
	for _, dir := range filepath.SplitList(config.SkillDirs.Get()) {
		if dir = strings.TrimSpace(dir); dir != "" {
			out = append(out, Source{Scope: ScopeExtra, Dir: dir})
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		for _, name := range wantedAgents(config.ExternalSkills.Get()) {
			out = append(out, Source{Scope: name, Dir: filepath.Join(append([]string{home}, agentSkillDirs[name]...)...)})
		}
	}
	return out
}

// SetSources adds folders besides the workspace's own. The workspace wins over all
// of them and they win over the built-in skills; between them, an earlier source
// wins over a later one.
func (l *Loader) SetSources(s []Source) {
	l.mu.Lock()
	l.sources = append([]Source(nil), s...)
	l.mu.Unlock()
}

// Sources returns what SetSources was given.
func (l *Loader) Sources() []Source {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]Source(nil), l.sources...)
}

func isSkillFile(name string) bool { return strings.EqualFold(name, "SKILL.md") }

// readSkillFile parses one SKILL.md.
func readSkillFile(path, scope string) (Skill, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, false
	}
	dir := filepath.Dir(path)
	meta, body := SplitFrontMatter(string(b))
	name := filepath.Base(dir)
	if n := strings.TrimSpace(meta.Name); n != "" {
		name = n
	}
	desc := strings.TrimSpace(meta.Description)
	if desc == "" {
		desc = firstParagraph(body)
	}
	return Skill{
		ID: "skill:" + name, Name: name, Path: path, Description: desc, Scope: scope, Content: body,
		Dir: dir, Requires: meta.Requires, Credentials: meta.Credentials,
	}, true
}

// skipDir names folders no skill lives in.
func skipDir(name string) bool { return name == "node_modules" || name == ".git" }

// scanDeep reads every SKILL.md below dir: the workspace's own folders, which the
// user fills by hand. Of two files naming the same skill the later one in walk
// order is kept.
func scanDeep(dir, scope string) map[string]Skill {
	found := map[string]Skill{}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isSkillFile(d.Name()) {
			if s, ok := readSkillFile(path, scope); ok {
				found[s.ID] = s
			}
		}
		return nil
	})
	return found
}

// maxShallowDepth: <dir>/<name>/SKILL.md and <dir>/<group>/<name>/SKILL.md.
const maxShallowDepth = 2

// scanShallow reads the skills of a folder that belongs to someone else: only a
// couple of levels deep, no hidden folders (Codex keeps its own under .system), and
// symlinked skill folders are followed because skill managers install that way.
func scanShallow(dir, scope string) map[string]Skill {
	found := map[string]Skill{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name, full := e.Name(), filepath.Join(dir, e.Name())
			isDir := e.IsDir()
			if e.Type()&fs.ModeSymlink != 0 {
				if st, err := os.Stat(full); err == nil {
					isDir = st.IsDir()
				}
			}
			switch {
			case isDir:
				if depth < maxShallowDepth && !strings.HasPrefix(name, ".") && !skipDir(name) {
					walk(full, depth+1)
				}
			case isSkillFile(name):
				if s, ok := readSkillFile(full, scope); ok {
					if _, dup := found[s.ID]; !dup {
						found[s.ID] = s
					}
				}
			}
		}
	}
	walk(dir, 0)
	return found
}
