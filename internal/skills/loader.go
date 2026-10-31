package skills

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

// Skill is a dynamically loaded workspace skill (SKILL.md).
type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Content     string `json:"content,omitempty"`
	// Dir is the folder of a workspace skill (empty for built-in ones); its scripts and references live there.
	Dir         string   `json:"dir,omitempty"`
	Requires    Requires `json:"requires"`
	Credentials []string `json:"credentials,omitempty"` // names only
}

// Loader scans workspace/skills and .cursor/skills for SKILL.md files and adds
// the skills built into the program.
type Loader struct {
	mu     sync.RWMutex
	root   string
	skills map[string]Skill

	credFile string // see SetCredentialFile
}

func NewLoader(workspaceRoot string) *Loader {
	return &Loader{root: workspaceRoot, skills: make(map[string]Skill)}
}

// Root is the workspace folder the loader scans.
func (l *Loader) Root() string { return l.root }

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
				meta, body := SplitFrontMatter(string(b))
				name := filepath.Base(dir)
				if n := strings.TrimSpace(meta.Name); n != "" {
					name = n
				}
				id := "skill:" + name
				desc := strings.TrimSpace(meta.Description)
				if desc == "" {
					desc = firstParagraph(body)
				}
				l.skills[id] = Skill{
					ID: id, Name: name, Path: path, Description: desc, Scope: "workspace", Content: body,
					Dir: dir, Requires: meta.Requires, Credentials: meta.Credentials,
				}
			}
			return nil
		})
	}
	// Built-in skills fill in what the workspace does not define itself.
	for _, s := range builtinSkills() {
		if _, taken := l.skills[s.ID]; !taken {
			l.skills[s.ID] = s
		}
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
		if len(s.Content) > MaxContextBytes {
			b.WriteString(ClipUTF8(s.Content, MaxContextBytes))
			b.WriteString("\n…(truncated)\n")
		} else {
			b.WriteString(s.Content)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// MaxContextBytes caps how much of one skill is put into the model's context.
// Chinese text takes three bytes a character, so the cap has to be generous.
const MaxContextBytes = 16000

// ClipUTF8 cuts s to at most n bytes without splitting a character.
func ClipUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
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
