package agent

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentgo/internal/config"
)

// Instruction files tell the agent how this user and this project want things
// done. They are found in the places the other coding agents look, so a project
// that already has them works as it is:
//
//   - AGENTS.md in the data directory: the user's own instructions for every workspace.
//   - AGENTS.md, else agents.md, else CLAUDE.md, in each directory from the git
//     root down to the workspace root; the more specific one comes later.
//   - SOUL.md and TEAM.md in the workspace root (AgentGo's persona and team files).
//   - .cursor/rules: rules that always apply are loaded as they are; rules that
//     only match a description or some files are listed in an index the agent
//     reads from when the task fits, so they cost a line each instead of a page.

// instructionNames are the file names that carry a directory's instructions, in
// order of preference; the first one that exists is the one that is used.
var instructionNames = []string{"AGENTS.md", "agents.md", "CLAUDE.md"}

const (
	// maxProjectAncestors bounds the walk up to the git root.
	maxProjectAncestors = 12
	// maxRuleIndexEntries bounds the on-demand rule index.
	maxRuleIndexEntries = 60
	// ruleHeadBytes is how much of a rule file is read to find its front matter.
	ruleHeadBytes = 8 << 10
)

// instructionSet is what the agentsmd middleware is given.
type instructionSet struct {
	Paths   []string          // files to load, in the order the model should read them
	Roots   []string          // directory trees whose files may be read, @imports included
	Files   []string          // single files that may be read although they lie outside Roots
	Virtual map[string]string // generated documents, by slash-separated path

	RulesDir string // the workspace's .cursor/rules; rule files lose their front matter when read
}

// discoverInstructions finds the instruction files for a workspace. dataDir may
// be empty. Without a workspace only the user-level file can apply.
func discoverInstructions(workspaceRoot, dataDir string) instructionSet {
	set := instructionSet{Virtual: map[string]string{}}
	var seen []os.FileInfo
	add := func(path string) bool {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return false
		}
		for _, other := range seen {
			if os.SameFile(info, other) { // the same file under another case or link
				return false
			}
		}
		seen = append(seen, info)
		set.Paths = append(set.Paths, path)
		return true
	}

	parents := config.AgentsMDParents.On()
	if parents && dataDir != "" {
		user := filepath.Join(dataDir, "AGENTS.md")
		if add(user) {
			set.Files = append(set.Files, user)
		}
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		return set
	}

	root := filepath.Clean(workspaceRoot)
	top := root
	if parents {
		top = projectRoot(root)
	}
	set.Roots = append(set.Roots, top)
	for _, dir := range dirsDownTo(top, root) {
		for _, name := range instructionNames {
			if add(filepath.Join(dir, name)) {
				break
			}
		}
	}
	for _, name := range []string{"SOUL.md", "TEAM.md"} {
		add(filepath.Join(root, name))
	}

	set.RulesDir = filepath.Join(root, ".cursor", "rules")
	var onDemand []ruleFile
	for _, rule := range scanRules(root) {
		switch rule.kind() {
		case ruleAlways:
			add(rule.path)
		case ruleOnDemand:
			onDemand = append(onDemand, rule)
		}
	}
	if len(onDemand) > 0 {
		index := ruleIndexPath(root)
		set.Virtual[filepath.ToSlash(index)] = ruleIndex(onDemand)
		set.Paths = append(set.Paths, index)
	}
	return set
}

// projectRoot is the nearest directory at or above start that holds a .git
// entry, or start itself when there is none within reach.
func projectRoot(start string) string {
	dir := start
	for i := 0; i < maxProjectAncestors; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return start
}

// dirsDownTo lists the directories from top down to and including dir, which
// must lie at or under top.
func dirsDownTo(top, dir string) []string {
	var chain []string
	for current := dir; ; current = filepath.Dir(current) {
		chain = append(chain, current)
		if sameDir(current, top) || filepath.Dir(current) == current {
			break
		}
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

func sameDir(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(ai, bi)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

type ruleKind int

const (
	ruleAlways   ruleKind = iota // loaded as it is
	ruleOnDemand                 // listed in the index
	ruleManual                   // not offered; the user names it when they want it
)

type ruleFile struct {
	path string // absolute
	rel  string // slash-separated, from the workspace root
	meta ruleMeta
}

// kind decides how a rule is offered. A rule with no front matter, or with
// alwaysApply: true, always applies; this keeps the behavior of rule files that
// predate front matter. One that has a description or file patterns is offered
// when it fits the task. alwaysApply: false with neither is for manual use.
func (r ruleFile) kind() ruleKind {
	m := r.meta
	switch {
	case !m.Present:
		return ruleAlways
	case m.AlwaysApply != nil && *m.AlwaysApply:
		return ruleAlways
	case m.Description != "" || len(m.Globs) > 0:
		return ruleOnDemand
	case m.AlwaysApply != nil:
		return ruleManual
	default:
		return ruleAlways
	}
}

// scanRules lists the rule files under <root>/.cursor/rules in path order.
func scanRules(root string) []ruleFile {
	dir := filepath.Join(root, ".cursor", "rules")
	var rules []ruleFile
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if ext := filepath.Ext(path); ext != ".mdc" && ext != ".md" {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		meta, _ := parseRuleFile(readHead(path, ruleHeadBytes))
		rules = append(rules, ruleFile{path: path, rel: filepath.ToSlash(rel), meta: meta})
		return nil
	})
	sort.Slice(rules, func(i, j int) bool { return rules[i].rel < rules[j].rel })
	return rules
}

// readHead returns up to n bytes from the start of a file ("" when unreadable).
func readHead(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf, _ := io.ReadAll(io.LimitReader(f, int64(n)))
	return string(buf)
}

// ruleIndexPath names the generated index document. It is a path only so that
// the loader can print it in its heading; nothing by that name exists on disk.
func ruleIndexPath(root string) string {
	return filepath.Join(root, ".cursor", "rules", "(rules to read when relevant)")
}

func ruleIndex(rules []ruleFile) string {
	var b strings.Builder
	b.WriteString("These project rules are not loaded into every conversation. When the task matches a rule's description or its file patterns, " +
		"read that rule file first (with the file view tool) and follow it:\n")
	for i, r := range rules {
		if i == maxRuleIndexEntries {
			fmt.Fprintf(&b, "- ... and %d more rules in .cursor/rules\n", len(rules)-i)
			break
		}
		b.WriteString("- " + r.rel)
		if d := oneLine(r.meta.Description, 200); d != "" {
			b.WriteString(" - " + d)
		}
		if len(r.meta.Globs) > 0 {
			b.WriteString(" (files: " + strings.Join(r.meta.Globs, ", ") + ")")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// oneLine flattens s to one line of at most max runes.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "..."
	}
	return s
}
