package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/agentsmd"
)

// maxInstructionRead bounds how much of one instruction file is read. The
// loader cuts files much shorter; this only keeps a wrong path (a huge file, a
// device) from being read in full.
const maxInstructionRead = 1 << 20

// instructionBackend implements agentsmd.Backend without eino-ext/local (no
// go-fitz). It serves the instruction files discoverInstructions found, the
// documents it generated, and what those files @import from the project; nothing
// else. A file that is not allowed is reported as missing, which makes the
// loader skip it: an error of any other kind would end the whole load, and with
// it the run.
type instructionBackend struct {
	workDir  string   // relative paths are read from here
	roots    []string // allowed directory trees, symlinks resolved
	files    []string // allowed single files, symlinks resolved
	rulesDir string   // files under it lose their front matter
	virtual  map[string]string
}

func newInstructionBackend(workDir string, set instructionSet) agentsmd.Backend {
	b := &instructionBackend{workDir: workDir, virtual: set.Virtual}
	for _, root := range set.Roots {
		b.roots = append(b.roots, resolvedPath(root))
	}
	for _, file := range set.Files {
		b.files = append(b.files, resolvedPath(file))
	}
	if set.RulesDir != "" {
		b.rulesDir = resolvedPath(set.RulesDir)
	}
	return b
}

// resolvedPath is p with symbolic links followed, or p cleaned when it cannot be.
func resolvedPath(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

func (b *instructionBackend) Read(_ context.Context, req *agentsmd.ReadRequest) (*agentsmd.FileContent, error) {
	path := filepath.Clean(req.FilePath)
	if text, ok := b.virtual[filepath.ToSlash(path)]; ok {
		return fileContent(text), nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(b.workDir, path)
	}
	// Decide on where the file really is: a link inside the project that leads
	// out of it must not read what lies behind it.
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err // a missing file wraps os.ErrNotExist and is skipped
	}
	if !b.allowed(real) {
		log.Printf("[agentsmd] %s lies outside the project and the user-level instruction file; not loaded", path)
		return nil, fmt.Errorf("%w: %s is outside the instruction locations", os.ErrNotExist, path)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, err
	}
	if info.IsDir() { // "@docs/" in a file names a directory, not a file
		return nil, fmt.Errorf("%w: %s is a directory", os.ErrNotExist, path)
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxInstructionRead))
	if err != nil {
		return nil, err
	}
	text := string(data)
	if b.rulesDir != "" && insideDir(b.rulesDir, real) {
		_, text = parseRuleFile(text)
	}
	return fileContent(text), nil
}

func (b *instructionBackend) allowed(real string) bool {
	for _, file := range b.files {
		if rel, err := filepath.Rel(file, real); err == nil && rel == "." {
			return true
		}
	}
	for _, root := range b.roots {
		if insideDir(root, real) {
			return true
		}
	}
	return false
}

// insideDir reports whether path lies strictly under dir.
func insideDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func fileContent(text string) *agentsmd.FileContent {
	fc := filesystem.FileContent{Content: text}
	return (*agentsmd.FileContent)(&fc)
}
