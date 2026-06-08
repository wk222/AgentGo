package skills

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Skill is a dynamically loaded workspace skill (SKILL.md).
type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Content     string `json:"content,omitempty"`
}

// Loader scans workspace/skills and .cursor/skills for SKILL.md files.
type Loader struct {
	mu     sync.RWMutex
	root   string
	skills map[string]Skill
}

func NewLoader(workspaceRoot string) *Loader {
	return &Loader{root: workspaceRoot, skills: make(map[string]Skill)}
}

func (l *Loader) Reload() []Skill {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.skills = make(map[string]Skill)
	dirs := []string{
		filepath.Join(l.root, "skills"),
		filepath.Join(l.root, ".cursor", "skills"),
	}
	for _, base := range dirs {
		_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if strings.EqualFold(d.Name(), "SKILL.md") || strings.EqualFold(d.Name(), "skill.md") {
				b, err := os.ReadFile(path)
				if err != nil {
					return nil
				}
				dir := filepath.Dir(path)
				name := filepath.Base(dir)
				id := "skill:" + name
				desc := firstParagraph(string(b))
				l.skills[id] = Skill{
					ID: id, Name: name, Path: path, Description: desc, Scope: "workspace", Content: string(b),
				}
			}
			return nil
		})
	}
	out := make([]Skill, 0, len(l.skills))
	for _, s := range l.skills {
		out = append(out, s)
	}
	return out
}

func (l *Loader) Get(id string) (Skill, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	s, ok := l.skills[id]
	return s, ok
}

func (l *Loader) List() []Skill {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Skill, 0, len(l.skills))
	for _, s := range l.skills {
		out = append(out, s)
	}
	return out
}

func (l *Loader) ContextBlock(activeIDs []string) string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var b strings.Builder
	for _, id := range activeIDs {
		s, ok := l.skills[id]
		if !ok {
			continue
		}
		b.WriteString("\n## Skill: ")
		b.WriteString(s.Name)
		b.WriteString("\n")
		if len(s.Content) > 4000 {
			b.WriteString(s.Content[:4000])
			b.WriteString("\n…(truncated)\n")
		} else {
			b.WriteString(s.Content)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func firstParagraph(s string) string {
	lines := strings.Split(s, "\n")
	var b strings.Builder
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			if b.Len() > 0 {
				break
			}
			continue
		}
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(ln)
		if b.Len() > 200 {
			break
		}
	}
	return b.String()
}
