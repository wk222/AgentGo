package codetools

// glob: find files by name pattern, newest first.

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"agentgo/internal/coderuntime"
	"agentgo/internal/processutil"
)

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

type globHit struct {
	path string
	mod  time.Time
}

func (s *searcher) globTool() coderuntime.Tool {
	return coderuntime.Tool{
		Name: "glob",
		Description: fmt.Sprintf("Find files whose path matches a glob pattern such as \"**/*.go\" or \"src/*.ts\"; "+
			"the most recently modified come first and at most %d are returned. Hidden files and ignored folders are left out. "+
			"To search inside files, use grep.", s.Limit),
		Schema: objSchema(map[string]any{
			"pattern": prop("string", "Glob pattern the path (relative to the search folder) must match"),
			"path":    prop("string", "Folder to search; defaults to the working directory"),
		}, "pattern"),
		Parallel: true,
		Run: func(ctx context.Context, sessionID, callID, argsJSON string) (coderuntime.ToolResult, error) {
			var a globArgs
			if r, bad := decode(argsJSON, &a); bad {
				return r, nil
			}
			return s.glob(ctx, sessionID, callID, a), nil
		},
	}
}

func (s *searcher) glob(ctx context.Context, sessionID, callID string, a globArgs) coderuntime.ToolResult {
	if a.Pattern == "" {
		return toolErr("pattern is required.")
	}
	if !doublestar.ValidatePattern(filepath.ToSlash(a.Pattern)) {
		return toolErr(fmt.Sprintf("%q is not a valid glob pattern.", a.Pattern))
	}
	root := s.resolveRoot(a.Path)
	walkRoot, rest := anchorGlob(root, a.Pattern)
	// The literal start of a pattern can lead out of the search folder, so the
	// gate looks at where the search will really begin.
	gated := []string{root}
	if walkRoot != root {
		gated = append(gated, walkRoot)
	}
	for _, dir := range gated {
		if msg, ok := s.allowRoot(ctx, sessionID, callID, "glob", "search", dir); !ok {
			return toolErr(msg)
		}
	}

	ctx, cancel := context.WithTimeout(ctx, s.GlobTimeout)
	defer cancel()
	files, truncated := s.findFiles(ctx, walkRoot, rest)
	if len(files) == 0 {
		return toolOK(fmt.Sprintf("No files match %q under %s.", a.Pattern, filepath.ToSlash(walkRoot)))
	}

	var b strings.Builder
	if truncated {
		fmt.Fprintf(&b, "%d files shown, truncated: more than %d match, so this is not the total. Use a more specific pattern or path.\n\n", len(files), s.Limit)
	} else {
		fmt.Fprintf(&b, "%s match, newest first.\n\n", plural(len(files), "file", "files"))
	}
	for _, f := range files {
		b.WriteString(filepath.ToSlash(f))
		b.WriteByte('\n')
	}
	return toolOK(b.String())
}

// anchorGlob moves the literal leading folders of a pattern into the search
// root, so "internal/agent/*.go" searches internal/agent for "*.go". A pattern
// that starts at a drive or slash replaces the root altogether. A pattern with no
// wildcard at all keeps its final element as the thing to match.
func anchorGlob(root, pattern string) (walkRoot, rest string) {
	segs := strings.Split(filepath.ToSlash(pattern), "/")
	cut := len(segs) - 1 // keep at least the last element as the pattern
	for i, seg := range segs {
		if strings.ContainsAny(seg, `*?[{\`) {
			cut = i
			break
		}
	}
	prefix, rest := strings.Join(segs[:cut], "/"), strings.Join(segs[cut:], "/")
	switch {
	case prefix == "" && strings.HasPrefix(filepath.ToSlash(pattern), "/"):
		return string(filepath.Separator), rest // a pattern like "/*.txt"
	case prefix == "":
		return root, rest
	case isRooted(prefix) || filepath.VolumeName(prefix) != "":
		return filepath.FromSlash(prefix), rest
	}
	return filepath.Join(root, filepath.FromSlash(prefix)), rest
}

// findFiles lists the files below root whose relative path matches pattern,
// newest first and capped at the search limit. It uses ripgrep when available.
func (s *searcher) findFiles(ctx context.Context, root, pattern string) (files []string, truncated bool) {
	var hits []globHit
	ok := false
	gather := poolFor(s.Limit)
	if rg := s.Rg(); rg != "" {
		hits, ok = filesViaRipgrep(ctx, rg, root, pattern, gather)
	}
	if !ok {
		hits = filesViaWalk(ctx, root, pattern, gather)
	}
	sort.Slice(hits, func(i, j int) bool {
		if !hits[i].mod.Equal(hits[j].mod) {
			return hits[i].mod.After(hits[j].mod)
		}
		return hits[i].path < hits[j].path
	})
	truncated = ctx.Err() != nil || len(hits) > s.Limit
	if len(hits) > s.Limit {
		hits = hits[:s.Limit]
	}
	files = make([]string, len(hits))
	for i, h := range hits {
		files[i] = h.path
	}
	return files, truncated
}

// filesViaRipgrep lists with `rg --files`, stopping once the pool is full so a
// huge tree cannot exhaust memory. ok is false when ripgrep could not be used.
func filesViaRipgrep(ctx context.Context, rg, root, pattern string, pool int) (hits []globHit, ok bool) {
	args := append([]string{"--files", "--null", "--no-require-git"}, rgIgnoreFlags(root)...)
	if pattern != "" {
		args = append(args, "--glob", "/"+strings.TrimPrefix(pattern, "/"))
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(runCtx, rg, args...)
	processutil.HideWindow(cmd)
	cmd.Dir = root
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if cmd.Start() != nil {
		return nil, false
	}

	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	sc.Split(splitNUL)
	full := false
	for sc.Scan() {
		rel := sc.Text()
		if rel == "" {
			continue
		}
		abs := rel
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, rel)
		}
		if inNoiseDir(root, abs) {
			continue
		}
		fi, err := os.Stat(abs)
		if err != nil || fi.IsDir() {
			continue
		}
		hits = append(hits, globHit{path: abs, mod: fi.ModTime()})
		if len(hits) >= pool {
			full = true
			stop()
			break
		}
	}
	werr := cmd.Wait()
	if full || ctx.Err() != nil || werr == nil {
		return hits, true
	}
	var ee *exec.ExitError
	if errors.As(werr, &ee) && ee.ExitCode() == 1 {
		return hits, true // nothing matched
	}
	return nil, false
}

func splitNUL(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// filesViaWalk is the pure-Go listing used when ripgrep is missing.
func filesViaWalk(ctx context.Context, root, pattern string, pool int) []globHit {
	pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "/")
	skip := newIgnorer(root)
	skip.keepNoiseFiles = true // the model asked for these names; only folders and ignore files hide things
	var hits []globHit

	walkTree(ctx, root, walkOpts{}, func(p string, isDir bool) walkAction {
		if p == root {
			return walkContinue
		}
		hidden := strings.HasPrefix(filepath.Base(p), ".")
		if hidden || skip.ignored(p, isDir, nil) {
			if isDir {
				return walkSkipDir
			}
			return walkContinue
		}
		if isDir {
			return walkContinue
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return walkContinue
		}
		if ok, err := doublestar.Match(pattern, filepath.ToSlash(rel)); err != nil || !ok {
			return walkContinue
		}
		if fi, err := os.Stat(p); err == nil {
			hits = append(hits, globHit{path: p, mod: fi.ModTime()})
		}
		if len(hits) >= pool {
			return walkStop
		}
		return walkContinue
	})
	return hits
}
