package skills

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SKILL.md may start with a front matter block between two "---" lines:
//
//	---
//	name: weekly-report
//	description: Build the weekly sales report from the exported CSV files.
//	requires:
//	  bins: [python]
//	credentials: [SALES_API_TOKEN]
//	---
//
// name and description replace the folder name and the first paragraph. requires.bins are programs that
// must be on PATH. credentials are names of secrets the scripts need: the skill only names them; the value is
// looked up when a script runs (the process environment first, then the credentials file in the data folder)
// and handed to that one script's environment. It never reaches the model, the skill text or the arguments.

// Meta is the parsed front matter.
type Meta struct {
	Name        string   `yaml:"name" json:"name,omitempty"`
	Description string   `yaml:"description" json:"description,omitempty"`
	Requires    Requires `yaml:"requires" json:"requires"`
	Credentials []string `yaml:"credentials" json:"credentials,omitempty"`
}

// Requires is what must exist before a skill's scripts can run.
type Requires struct {
	Bins []string `yaml:"bins" json:"bins,omitempty"`
}

// SplitFrontMatter returns the front matter and the text after it. Text without a closing line, or whose
// block is not valid YAML, is returned unchanged with a zero Meta so a stray "---" in a document is harmless.
func SplitFrontMatter(content string) (Meta, string) {
	text := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Meta{}, content
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Meta{}, content
	}
	after := rest[end+len("\n---"):]
	if after != "" && !strings.HasPrefix(after, "\n") {
		return Meta{}, content // "----" or "---x" is not a closing line
	}
	var m Meta
	if err := yaml.Unmarshal([]byte(rest[:end]), &m); err != nil {
		return Meta{}, content
	}
	return m, strings.TrimLeft(after, "\n")
}

// Readiness says whether a skill's scripts can run here. It carries names only, never values.
type Readiness struct {
	OK                 bool     `json:"ok"`
	MissingBins        []string `json:"missing_bins,omitempty"`
	MissingCredentials []string `json:"missing_credentials,omitempty"`
}

// binAliases lists the names a program goes by on different systems.
var binAliases = map[string][]string{
	"python":  {"python", "python3", "py"},
	"python3": {"python3", "python", "py"},
	"pwsh":    {"pwsh"},
	"node":    {"node"},
}

// LookBin finds a program on PATH, trying the usual aliases.
func LookBin(name string) (string, bool) {
	names := binAliases[strings.ToLower(name)]
	if len(names) == 0 {
		names = []string{name}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p, true
		}
	}
	return "", false
}

// SetCredentialFile names the JSON file ({"NAME": "value"}) credentials are read from after the process
// environment. A missing file is fine.
func (l *Loader) SetCredentialFile(path string) {
	l.mu.Lock()
	l.credFile = path
	l.mu.Unlock()
}

// Credential returns the value of a named credential. Callers must not put it in model-visible text.
func (l *Loader) Credential(name string) (string, bool) {
	if v := os.Getenv(name); v != "" {
		return v, true
	}
	l.mu.RLock()
	path := l.credFile
	l.mu.RUnlock()
	if path == "" {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil {
		return "", false
	}
	v, ok := m[name]
	return v, ok && v != ""
}

// Check reports what a skill still needs before its scripts can run.
func (l *Loader) Check(s Skill) Readiness {
	r := Readiness{OK: true}
	for _, b := range s.Requires.Bins {
		if _, ok := LookBin(b); !ok {
			r.MissingBins = append(r.MissingBins, b)
		}
	}
	for _, c := range s.Credentials {
		if _, ok := l.Credential(c); !ok {
			r.MissingCredentials = append(r.MissingCredentials, c)
		}
	}
	r.OK = len(r.MissingBins) == 0 && len(r.MissingCredentials) == 0
	return r
}

// maxResources bounds the file list handed to the model.
const maxResources = 40

// Resources lists the files that ship with a workspace skill (its scripts and reference documents), relative to
// the skill's folder, without SKILL.md itself.
func Resources(s Skill) []string {
	if s.Dir == "" {
		return nil
	}
	var out []string
	_ = filepath.WalkDir(s.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != s.Dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(d.Name(), "SKILL.md") {
			return nil
		}
		if rel, err := filepath.Rel(s.Dir, path); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	if len(out) > maxResources {
		out = out[:maxResources]
	}
	return out
}
