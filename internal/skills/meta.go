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
		// Skills written for other agents often leave a colon unquoted in the
		// description, which YAML refuses. Name and description can still be read line by line.
		if m = lenientMeta(rest[:end]); m.Name == "" && m.Description == "" {
			return Meta{}, content
		}
	}
	return m, strings.TrimLeft(after, "\n")
}

// lenientMeta reads the top-level "name:" and "description:" lines of a block that is not valid YAML.
func lenientMeta(block string) Meta {
	var m Meta
	lines := strings.Split(block, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, val = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(val)
		if key != "name" && key != "description" {
			continue
		}
		if strings.HasPrefix(val, ">") || strings.HasPrefix(val, "|") {
			// A block scalar: the indented lines that follow are the value.
			var parts []string
			for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], " ") || strings.HasPrefix(lines[i+1], "\t") || strings.TrimSpace(lines[i+1]) == "") {
				i++
				if t := strings.TrimSpace(lines[i]); t != "" {
					parts = append(parts, t)
				}
			}
			val = strings.Join(parts, " ")
		}
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if key == "name" {
			m.Name = val
		} else {
			m.Description = val
		}
	}
	return m
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
