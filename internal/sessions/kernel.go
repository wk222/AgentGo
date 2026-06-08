package sessions

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
)

// SessionKernel tracks workspace recency, team memory hints, and compression triggers (PyBot SessionKernel subset).
type SessionKernel struct {
	mu          sync.Mutex
	root        string
	touched     map[string]int64 // rel path -> unix mod time when recorded
	maxRecent   int
	compressAt  int // active turn pairs before hygiene suggests compress
}

// NewSessionKernel creates a kernel for the given workspace root.
func NewSessionKernel(root string, maxRecent, compressAt int) *SessionKernel {
	if maxRecent <= 0 {
		maxRecent = 8
	}
	if compressAt <= 0 {
		compressAt = 45
	}
	return &SessionKernel{
		root:       strings.TrimSpace(root),
		touched:    make(map[string]int64),
		maxRecent:  maxRecent,
		compressAt: compressAt,
	}
}

// RecordFileTouch marks a workspace-relative path as recently used.
func (k *SessionKernel) RecordFileTouch(relPath string) {
	if k == nil || k.root == "" {
		return
	}
	relPath = normalizeRelPath(relPath)
	if relPath == "" {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.touched[relPath] = time.Now().Unix()
}

// EnrichSnapshot fills WorkspaceRecent, TeamMemory, and optional hygiene from kernel state.
func (k *SessionKernel) EnrichSnapshot(snap SpineSnapshot, messages []*schema.Message) SpineSnapshot {
	if k == nil {
		return snap
	}
	recent := k.recentFiles()
	for _, p := range recent {
		line := "recent: " + p
		if !containsLine(snap.WorkspaceRecent, line) {
			snap.WorkspaceRecent = append(snap.WorkspaceRecent, line)
		}
	}
	for _, line := range k.teamMemoryLines() {
		if !containsLine(snap.TeamMemory, line) {
			snap.TeamMemory = append(snap.TeamMemory, line)
		}
	}
	if k.shouldSuggestCompress(messages) && len(snap.EpisodicSummary) == 0 {
		hint := "活跃轮次较多，将触发 episodic 压缩（AGENTGO_EPISODIC_COMPRESS=1 强制开启）"
		if !containsLine(snap.ContextHygiene, hint) {
			snap.ContextHygiene = append(snap.ContextHygiene, hint)
		}
	}
	return snap
}

// ShouldCompress reports whether message volume warrants episodic compression.
func (k *SessionKernel) ShouldCompress(messages []*schema.Message) bool {
	if k == nil {
		return false
	}
	return k.shouldSuggestCompress(messages)
}

func (k *SessionKernel) recentFiles() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	type pair struct {
		path string
		ts   int64
	}
	var list []pair
	for p, ts := range k.touched {
		list = append(list, pair{p, ts})
	}
	if len(list) < k.maxRecent {
		for _, p := range k.scanMTIME() {
			found := false
			for _, x := range list {
				if x.path == p {
					found = true
					break
				}
			}
			if !found {
				list = append(list, pair{p, 0})
			}
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ts > list[j].ts })
	n := k.maxRecent
	if len(list) < n {
		n = len(list)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, list[i].path)
	}
	return out
}

func (k *SessionKernel) scanMTIME() []string {
	if k.root == "" {
		return nil
	}
	cutoff := time.Now().Add(-72 * time.Hour)
	var found []string
	_ = filepath.WalkDir(k.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() {
				rel, _ := filepath.Rel(k.root, path)
				if strings.Count(rel, string(os.PathSeparator)) >= 3 {
					return filepath.SkipDir
				}
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".go" && ext != ".md" && ext != ".py" && ext != ".ts" && ext != ".tsx" {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		rel, err := filepath.Rel(k.root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil
		}
		found = append(found, filepath.ToSlash(rel))
		if len(found) >= k.maxRecent*2 {
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func (k *SessionKernel) teamMemoryLines() []string {
	if k.root == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(k.root, "TEAM.md"))
	if err != nil || len(b) == 0 {
		return nil
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 600 {
		s = s[:600] + "…"
	}
	return []string{s}
}

func (k *SessionKernel) shouldSuggestCompress(messages []*schema.Message) bool {
	n := 0
	for _, m := range messages {
		if m == nil || m.Role == schema.System {
			continue
		}
		if m.Role == schema.User || m.Role == schema.Assistant {
			n++
		}
	}
	return n >= k.compressAt
}

func normalizeRelPath(p string) string {
	p = strings.TrimSpace(filepath.ToSlash(p))
	p = strings.TrimPrefix(p, "./")
	return p
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
