// Package evolution is the safety layer of measured harness self-improvement.
//
// It answers two questions before anything is run or adopted: what may an
// improver edit (the Surface), and what must never reach it or be baked into
// what it writes (the leak Guard). It is standard library only; running
// candidates and judging them belong to later steps.
//
// The surface is deliberately small. An improver may change workspace guidance
// (AGENTS.md) and skills (skills/<name>/SKILL.md). It may not change Go code,
// governance or approval settings, credentials or the evaluation suite: a loop
// that could edit its own gate would stop measuring anything.
package evolution

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Tree maps slash-separated paths, relative to the workspace root, to content.
type Tree map[string]string

const (
	// AgentsFile is the workspace guidance file the host injects into sessions.
	AgentsFile = "AGENTS.md"
	skillsDir  = "skills"
	skillFile  = "SKILL.md"

	// MaxSkillBytes mirrors skills.Loader.ContextBlock, which cuts a skill at
	// 4000 bytes: text past it is never shown to the model.
	MaxSkillBytes = 4000
)

// Limits caps a candidate. A value <= 0 means no cap.
type Limits struct {
	MaxFiles      int
	MaxFileBytes  int
	MaxTotalBytes int
}

// DefaultLimits is what an improver proposal is held to.
var DefaultLimits = Limits{MaxFiles: 40, MaxFileBytes: 32 << 10, MaxTotalBytes: 256 << 10}

var skillNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

const surfaceHelp = "only AGENTS.md and skills/<name>/SKILL.md are editable (<name>: lowercase letters, digits, _ or -)"

// Clean validates rel as a canonical slash path that stays inside the workspace.
func Clean(rel string) (string, error) {
	switch {
	case rel == "":
		return "", errors.New("empty path")
	case strings.ContainsAny(rel, "\\\x00"):
		return "", fmt.Errorf("%q: use forward slashes and no control characters", rel)
	case strings.HasPrefix(rel, "/") || (len(rel) >= 2 && rel[1] == ':'):
		return "", fmt.Errorf("%q: absolute paths are not allowed", rel)
	}
	c := path.Clean(rel)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("%q: escapes the workspace", rel)
	}
	if c != rel {
		return "", fmt.Errorf("%q: not a canonical path (want %q)", rel, c)
	}
	return c, nil
}

// Editable reports whether rel, already passed through Clean, is on the surface.
func Editable(rel string) bool {
	if rel == AgentsFile {
		return true
	}
	p := strings.Split(rel, "/")
	return len(p) == 3 && p[0] == skillsDir && skillNameRE.MatchString(p[1]) && p[2] == skillFile
}

func isSkill(rel string) bool { return strings.HasSuffix(rel, "/"+skillFile) }

// ReadTree reads the editable files under root. Anything else it meets is not
// applied and comes back in rejected (sorted), so the caller can tell the
// improver what was ignored. Symlinks are never followed, and a file larger
// than lim.MaxFileBytes is rejected unread.
func ReadTree(root string, lim Limits) (tree Tree, rejected []string, err error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, nil, err
	}
	if !st.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a directory", root)
	}
	tree = Tree{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if p == root {
			return nil
		}
		r, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel := filepath.ToSlash(r)
		if d.Type()&fs.ModeSymlink != 0 {
			rejected = append(rejected, rel+" (symlink)")
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !Editable(rel) {
			rejected = append(rejected, rel)
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if lim.MaxFileBytes > 0 && info.Size() > int64(lim.MaxFileBytes) {
			rejected = append(rejected, fmt.Sprintf("%s (%d bytes, over the %d limit)", rel, info.Size(), lim.MaxFileBytes))
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		tree[rel] = string(b)
		return nil
	})
	sort.Strings(rejected)
	if err != nil {
		return nil, rejected, err
	}
	return tree, rejected, nil
}

func sortedKeys(t Tree) []string {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// pathProblems reports every path in t that is not a clean, editable path.
func pathProblems(t Tree) []string {
	var out []string
	fold := map[string]string{}
	for _, rel := range sortedKeys(t) {
		c, err := Clean(rel)
		if err != nil {
			out = append(out, err.Error())
			continue
		}
		if !Editable(c) {
			out = append(out, fmt.Sprintf("%s is off the editable surface: %s", c, surfaceHelp))
			continue
		}
		key := strings.ToLower(c)
		if prev, dup := fold[key]; dup {
			out = append(out, fmt.Sprintf("%s and %s differ only by case", prev, c))
			continue
		}
		fold[key] = c
	}
	return out
}

func hasDescription(body string) bool {
	for _, ln := range strings.Split(body, "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" && !strings.HasPrefix(ln, "#") {
			return true
		}
	}
	return false
}

// Validate checks an improver's candidate. Every problem is reported at once,
// in prose, because the text is fed back to the improver as retry feedback.
func Validate(t Tree, lim Limits) error {
	var problems []string
	if len(t) == 0 {
		problems = append(problems, "the tree is empty; edit the files, do not delete them all")
	}
	if lim.MaxFiles > 0 && len(t) > lim.MaxFiles {
		problems = append(problems, fmt.Sprintf("%d files; at most %d are allowed", len(t), lim.MaxFiles))
	}
	problems = append(problems, pathProblems(t)...)
	total := 0
	for _, rel := range sortedKeys(t) {
		if c, err := Clean(rel); err != nil || !Editable(c) {
			continue // already reported
		}
		body := t[rel]
		total += len(body)
		switch {
		case strings.TrimSpace(body) == "":
			problems = append(problems, rel+" is empty")
			continue
		case !utf8.ValidString(body) || strings.ContainsRune(body, 0):
			problems = append(problems, rel+" is not valid UTF-8 text")
			continue
		}
		if lim.MaxFileBytes > 0 && len(body) > lim.MaxFileBytes {
			problems = append(problems, fmt.Sprintf("%s is %d bytes; the limit is %d", rel, len(body), lim.MaxFileBytes))
		}
		if isSkill(rel) {
			if len(body) > MaxSkillBytes {
				problems = append(problems, fmt.Sprintf(
					"%s is %d bytes; the skill loader shows the model only the first %d, so the rest is dead text. Shorten it or split it into two skills",
					rel, len(body), MaxSkillBytes))
			}
			if !hasDescription(body) {
				problems = append(problems, rel+" has no description paragraph; the first non-heading paragraph is what the skill list shows")
			}
		}
	}
	if lim.MaxTotalBytes > 0 && total > lim.MaxTotalBytes {
		problems = append(problems, fmt.Sprintf("%d bytes in total; the limit is %d", total, lim.MaxTotalBytes))
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New("invalid harness:\n- " + strings.Join(problems, "\n- "))
}

// Change summarises how a candidate differs from its base.
type Change struct{ Added, Removed, Modified []string }

// Empty reports that the candidate is identical to its base.
func (c Change) Empty() bool { return len(c.Added)+len(c.Removed)+len(c.Modified) == 0 }

func (c Change) String() string {
	return fmt.Sprintf("+%d -%d ~%d", len(c.Added), len(c.Removed), len(c.Modified))
}

// Diff compares two trees by path and content.
func Diff(base, next Tree) Change {
	var c Change
	for _, k := range sortedKeys(next) {
		old, ok := base[k]
		switch {
		case !ok:
			c.Added = append(c.Added, k)
		case old != next[k]:
			c.Modified = append(c.Modified, k)
		}
	}
	for _, k := range sortedKeys(base) {
		if _, ok := next[k]; !ok {
			c.Removed = append(c.Removed, k)
		}
	}
	return c
}

// refuseSymlinks fails when any existing component of root/rel is a symlink, so
// a write can never be redirected out of the workspace.
func refuseSymlinks(root, rel string) error {
	cur := root
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; refusing to write through it", rel)
		}
	}
	return nil
}

// WriteTree makes the editable surface under root equal to t: files in t are
// written, editable files not in t are removed, everything else is untouched.
// A skill directory is removed only when removing its SKILL.md leaves it empty.
func WriteTree(root string, t Tree) error {
	if p := pathProblems(t); len(p) > 0 {
		return errors.New("cannot write tree:\n- " + strings.Join(p, "\n- "))
	}
	cur, _, err := ReadTree(root, Limits{})
	if err != nil {
		return err
	}
	for _, rel := range sortedKeys(cur) {
		if _, keep := t[rel]; keep {
			continue
		}
		if err := refuseSymlinks(root, rel); err != nil {
			return err
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if isSkill(rel) {
			_ = os.Remove(filepath.Dir(full)) // fails, harmlessly, when other files remain
		}
	}
	for _, rel := range sortedKeys(t) {
		if err := refuseSymlinks(root, rel); err != nil {
			return err
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(t[rel]), 0o644); err != nil {
			return err
		}
	}
	return nil
}
