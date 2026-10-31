package evolution

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// MinForbiddenLen is the shortest string a Guard will enforce. Anything shorter
// ("ok", "file") is too common to mean "this text came from the judge" and would
// only produce false alarms.
const MinForbiddenLen = 8

// maxScanBytes bounds one scanned file; a bigger one is refused rather than
// skipped, because a skipped file is a place for a leak to hide.
const maxScanBytes = 8 << 20

// VerdictMarkers are phrases the eval runner itself writes into failure
// messages (internal/evaluation/case_run.go). They are the judge's voice.
var VerdictMarkers = []string{
	"compromised:",
	"cases that are not marked expect_approval",
	"expected the run to pause for approval",
	"did not match expectations",
	"answer was not grounded in tools",
	"tool calls, limit",
}

// textSuffixes are the file types a directory scan reads; the rest are binary or
// opaque and are not interpreted.
var textSuffixes = map[string]bool{
	"": true, ".md": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true,
	".csv": true, ".tsv": true, ".log": true, ".html": true, ".htm": true,
	".diff": true, ".patch": true, ".toml": true, ".ini": true, ".xml": true,
}

type forbidden struct{ text, norm, why string }

// Guard refuses text that would let an improver see, or bake into a harness,
// what the judge knows. Held-out expectations and the runner's verdict phrases
// are the two things to keep out; Forbid and ForbidVerdictMarkers register them.
type Guard struct {
	items   []forbidden
	skipped int
}

// NewGuard returns an empty Guard.
func NewGuard() *Guard { return &Guard{} }

// norm lower-cases and collapses whitespace, so a case change or a line wrap
// does not hide a match.
func norm(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// Forbid registers strings that must not appear. Strings shorter than
// MinForbiddenLen after normalisation are not enforced and are counted in Skipped.
func (g *Guard) Forbid(why string, texts ...string) {
	for _, t := range texts {
		n := norm(t)
		if utf8.RuneCountInString(n) < MinForbiddenLen {
			g.skipped++
			continue
		}
		g.items = append(g.items, forbidden{text: strings.TrimSpace(t), norm: n, why: why})
	}
}

// ForbidVerdictMarkers registers the runner's failure phrases.
func (g *Guard) ForbidVerdictMarkers() { g.Forbid("eval verdict text", VerdictMarkers...) }

// Skipped is how many registered strings were too short to enforce.
func (g *Guard) Skipped() int { return g.skipped }

// Len is how many strings are enforced.
func (g *Guard) Len() int { return len(g.items) }

// ScanText checks one named body.
func (g *Guard) ScanText(name, body string) error {
	n := norm(body)
	for _, it := range g.items {
		i := strings.Index(n, it.norm)
		if i < 0 {
			continue
		}
		return fmt.Errorf("%s contains %s %q (near %q). This is a leak, not a false positive to widen around: remove that text, or find the read path that produced it",
			name, it.why, it.text, snippet(n, i, len(it.norm)))
	}
	return nil
}

func snippet(s string, at, n int) string {
	from, to := at-30, at+n+30
	if from < 0 {
		from = 0
	}
	if to > len(s) {
		to = len(s)
	}
	for from > 0 && !utf8.RuneStart(s[from]) {
		from--
	}
	for to < len(s) && !utf8.RuneStart(s[to]) {
		to++
	}
	return s[from:to]
}

// ScanTree checks every file name and body of a candidate harness.
func (g *Guard) ScanTree(t Tree) error {
	for _, rel := range sortedKeys(t) {
		if err := g.ScanText("file name "+rel, rel); err != nil {
			return err
		}
		if err := g.ScanText(rel, t[rel]); err != nil {
			return err
		}
	}
	return nil
}

// ScanDir checks the names and text-file bodies under root, which is how an
// evidence directory is verified before an improver is pointed at it.
func (g *Guard) ScanDir(root string) error {
	if len(g.items) == 0 {
		return nil
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		r, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel := filepath.ToSlash(r)
		if err := g.ScanText("file name "+rel, rel); err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || !textSuffixes[strings.ToLower(filepath.Ext(rel))] {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if info.Size() > maxScanBytes {
			return errors.New(rel + " is too large to scan for leaks; split it or leave it out of the evidence")
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return g.ScanText(rel, string(b))
	})
}
