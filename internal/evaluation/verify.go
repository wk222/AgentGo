package evaluation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// EvalFile is a fixture file written into the workspace before a run.
type EvalFile struct {
	Path    string `json:"path"` // relative to the workspace, inside Scratch
	Content string `json:"content"`
}

// EvalCheck is an assertion about the workspace after a run. A model's claim
// that it "wrote the file" says nothing; the disk does.
type EvalCheck struct {
	Path        string `json:"path"`                   // relative to the workspace
	Absent      bool   `json:"absent,omitempty"`       // the file must not exist (a denied write)
	Contains    string `json:"contains,omitempty"`     // substring the file must contain
	NotContains string `json:"not_contains,omitempty"` // substring the file must not contain
}

// EvalCommand is run in the workspace after a run; exit code 0 passes. The
// suite is trusted input: it executes on the machine that runs agentgo-eval.
type EvalCommand struct {
	Dir     string   `json:"dir,omitempty"` // relative to the workspace
	Name    string   `json:"name"`
	Args    []string `json:"args,omitempty"`
	Timeout int      `json:"timeout_sec,omitempty"` // default 120
}

// Validate rejects suites whose cases would silently do the wrong thing (a
// misspelt decision would otherwise be read as a denial).
func (s EvalSet) Validate() error {
	seen := map[string]bool{}
	for _, c := range s.Cases {
		if c.ID == "" || seen[c.ID] {
			return fmt.Errorf("case id %q is empty or duplicated", c.ID)
		}
		seen[c.ID] = true
		switch c.Decision {
		case "", "approve", "deny":
		default:
			return fmt.Errorf("case %s: decision %q must be \"approve\" or \"deny\"", c.ID, c.Decision)
		}
		if c.Decision != "" && c.ExpectApproval {
			return fmt.Errorf("case %s: decision and expect_approval contradict each other", c.ID)
		}
		if len(c.Setup) > 0 && c.Scratch == "" {
			return fmt.Errorf("case %s: setup files need a scratch directory", c.ID)
		}
	}
	return nil
}

func (c EvalCase) hasVerification() bool { return len(c.Verify) > 0 || c.VerifyCmd != nil }

// needsWorkspace reports whether the case touches the workspace on disk.
func (c EvalCase) needsWorkspace() bool { return len(c.Setup) > 0 || c.hasVerification() || c.Scratch != "" }

// inside resolves rel under root and rejects anything that escapes it.
func inside(root, rel string) (string, error) {
	if rel == "" || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("path %q must be relative and stay inside the workspace", rel)
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// prepareWorkspace clears the case's scratch directory and writes its fixture.
// The returned func removes the scratch directory again.
func (c EvalCase) prepareWorkspace() (func(), error) {
	noop := func() {}
	if !c.needsWorkspace() {
		return noop, nil
	}
	if c.root == "" {
		return noop, errors.New("case verifies the workspace on disk: pass --workspace (the directory `agentgo --serve` runs in)")
	}
	if len(c.Setup) > 0 && c.Scratch == "" {
		return noop, errors.New("case has setup files but no scratch directory to clean up")
	}
	if c.Scratch == "" {
		return noop, nil
	}
	scratch, err := inside(c.root, c.Scratch)
	if err != nil {
		return noop, err
	}
	if filepath.Clean(scratch) == filepath.Clean(c.root) {
		return noop, errors.New("scratch must be a sub-directory of the workspace")
	}
	cleanup := func() { _ = os.RemoveAll(scratch) }
	cleanup()
	for _, f := range c.Setup {
		p, err := inside(c.root, f.Path)
		if err != nil {
			return noop, err
		}
		if rel, err := filepath.Rel(scratch, p); err != nil || !filepath.IsLocal(rel) {
			return noop, fmt.Errorf("setup file %q is outside scratch %q", f.Path, c.Scratch)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return noop, err
		}
		if err := os.WriteFile(p, []byte(f.Content), 0o644); err != nil {
			return noop, err
		}
	}
	return cleanup, nil
}

// verify checks the case's disk expectations and returns the first failure.
func (c EvalCase) verify(ctx context.Context) error {
	for _, ck := range c.Verify {
		p, err := inside(c.root, ck.Path)
		if err != nil {
			return err
		}
		raw, rerr := os.ReadFile(p)
		if ck.Absent {
			if rerr == nil {
				return fmt.Errorf("verify: %s exists but should not", ck.Path)
			}
			continue
		}
		if rerr != nil {
			return fmt.Errorf("verify: %s: %v", ck.Path, rerr)
		}
		text := string(raw)
		if ck.Contains != "" && !strings.Contains(text, ck.Contains) {
			return fmt.Errorf("verify: %s does not contain %q", ck.Path, ck.Contains)
		}
		if ck.NotContains != "" && strings.Contains(text, ck.NotContains) {
			return fmt.Errorf("verify: %s still contains %q", ck.Path, ck.NotContains)
		}
	}
	vc := c.VerifyCmd
	if vc == nil {
		return nil
	}
	dir := c.root
	if vc.Dir != "" {
		d, err := inside(c.root, vc.Dir)
		if err != nil {
			return err
		}
		dir = d
	}
	secs := vc.Timeout
	if secs <= 0 {
		secs = 120
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(secs)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, vc.Name, vc.Args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(out.String())
		if len(tail) > 600 {
			tail = "..." + tail[len(tail)-600:]
		}
		return fmt.Errorf("verify: `%s %s` failed: %v\n%s", vc.Name, strings.Join(vc.Args, " "), err, tail)
	}
	return nil
}
