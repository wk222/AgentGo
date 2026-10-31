package coderuntime

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"agentgo/internal/config"
	"agentgo/internal/processutil"
)

// Two safety nets under write/edit/multiedit, adapted from octo-agent's
// write_guard.go and overwrite_backup.go (MIT):
//
//   - a credential guard that refuses to let the agent author a live secret
//     into a file, and
//   - a backup of the previous bytes before any overwrite, so an edit that
//     destroys uncommitted work is recoverable.
//
// The read-before-write check these complement lives in staleCheck.

// secretPatterns are high-confidence credential shapes. The set is
// deliberately narrow: a missed low-confidence match is cheaper than blocking
// legitimate writes (docs, fixtures). Bare AWS access-key IDs are left out on
// purpose: the ID alone is not secret and the canonical docs example would
// trip on every tutorial.
var secretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"private key block", regexp.MustCompile(`-----BEGIN (?:RSA |OPENSSH |EC |DSA |PGP )?PRIVATE KEY-----`)},
	{"AWS secret access key", regexp.MustCompile(`(?i)aws_secret_access_key\s*[=:]\s*['"]?[A-Za-z0-9/+]{40}`)},
	{"GitHub token", regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[0-9A-Za-z]{36}\b`)},
	{"GitHub fine-grained token", regexp.MustCompile(`\bgithub_pat_[0-9A-Za-z_]{60,}\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}`)},
	{"OpenAI API key", regexp.MustCompile(`\bsk-[a-zA-Z0-9]{20,}\b`)},
	{"Anthropic API key", regexp.MustCompile(`\bsk-ant-[a-zA-Z0-9_-]{20,}\b`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}\b`)},
	{"JWT", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*\b`)},
	{"generic API key", regexp.MustCompile(`(?i)\b(?:api[_-]?key|apikey|secret[_-]?key)\s*[=:]\s*['"]?[a-zA-Z0-9_\-]{16,}\b`)},
}

// newSecretsIntroduced names the first secret shape present in newContent but
// not already present verbatim in oldContent. Preserving a credential the
// agent did not author (a read-modify-write of a config that already holds an
// api_key) is allowed; adding or changing one is not.
func newSecretsIntroduced(oldContent, newContent string) string {
	for _, p := range secretPatterns {
		newHits := p.re.FindAllString(newContent, -1)
		if len(newHits) == 0 {
			continue
		}
		old := make(map[string]bool, len(newHits))
		for _, h := range p.re.FindAllString(oldContent, -1) {
			old[h] = true
		}
		for _, h := range newHits {
			if !old[h] {
				return p.name
			}
		}
	}
	return ""
}

func secretGuardOff(o FSOptions) bool {
	return o.DisableSecretGuard || config.AllowSecretWrites.On()
}

// guardContent returns a refusal when newContent would introduce a secret.
func (t *fsTools) guardContent(path, oldContent, newContent string) *ToolResult {
	if secretGuardOff(t.opts) {
		return nil
	}
	name := newSecretsIntroduced(oldContent, newContent)
	if name == "" {
		return nil
	}
	r := errResult(fmt.Sprintf(
		"Refusing to write %s: the new content contains what looks like a %s. "+
			"Do not author credentials into files. Use a placeholder or an environment variable reference and tell the user which value to fill in themselves.",
		path, name))
	return &r
}

const (
	maxBackupBytes = 20 << 20
	maxBackupFiles = 200
)

// backupBeforeOverwrite copies the current bytes of path into BackupDir and
// returns the backup path ("" when nothing was saved). Best effort: it never
// blocks the write. Skipped for a missing file (a create), for files already
// inside the backup dir, for oversized files, and for files git can restore
// exactly (tracked with no staged or unstaged changes).
func (t *fsTools) backupBeforeOverwrite(path string) string {
	dir := t.opts.BackupDir
	if dir == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxBackupBytes {
		return ""
	}
	if rel, err := filepath.Rel(dir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return ""
	}
	if gitTrackedClean(path) {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	sum := sha1.Sum([]byte(filepath.Clean(path)))
	name := fmt.Sprintf("%s-%s-%s", time.Now().Format("20060102-150405.000"), hex.EncodeToString(sum[:3]), filepath.Base(path))
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return ""
	}
	pruneBackups(dir)
	return dst
}

// pruneBackups keeps the newest maxBackupFiles entries.
func pruneBackups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= maxBackupFiles {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // timestamp prefix: oldest first
	for _, n := range names[:max(0, len(names)-maxBackupFiles)] {
		_ = os.Remove(filepath.Join(dir, n))
	}
}

// gitTrackedClean reports whether git can restore path's exact bytes.
// Anything uncertain (untracked, dirty, no repo, no git) returns false so the
// caller backs up.
func gitTrackedClean(path string) bool {
	dir := filepath.Dir(path)
	for _, args := range [][]string{
		{"ls-files", "--error-unmatch", "--", path},
		{"diff", "--quiet", "--", path},
		{"diff", "--cached", "--quiet", "--", path},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		processutil.HideWindow(cmd)
		if err := cmd.Run(); err != nil {
			return false
		}
	}
	return true
}

// backupNote is appended to a successful result so the model (and user) know
// where the previous version went.
func backupNote(dst string) string {
	if dst == "" {
		return ""
	}
	return " (previous version saved to " + dst + ")"
}
