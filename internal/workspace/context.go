package workspace

import (
	"context"
	"fmt"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const ExtraWorkspaceContext = "agentgo.workspace_context"
const maxRuleFileBytes = 32 * 1024
const maxWorkspaceContextBytes = 96 * 1024

type cachedRule struct {
	info    os.FileInfo
	content string
}

// WorkspaceManager caches unchanged rule contents; stat and directory listing
// detect edits, removals and new rule files without rereading every document.
type WorkspaceManager struct {
	rootDir string
	mu      sync.Mutex
	files   map[string]cachedRule
	reads   int
}

func NewWorkspaceManager(rootDir string) *WorkspaceManager {
	return &WorkspaceManager{rootDir: rootDir, files: map[string]cachedRule{}}
}

func (wm *WorkspaceManager) BuildContext() string {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	var sb strings.Builder
	seen := map[string]bool{}
	add := func(rel, title string) {
		if sb.Len() >= maxWorkspaceContextBytes {
			return
		}
		path := filepath.Join(wm.rootDir, rel)
		seen[path] = true
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			delete(wm.files, path)
			return
		}
		entry, ok := wm.files[path]
		if !ok || entry.info.Size() != info.Size() || !entry.info.ModTime().Equal(info.ModTime()) || !os.SameFile(entry.info, info) {
			f, err := os.Open(path)
			if err != nil {
				delete(wm.files, path)
				return
			}
			b, err := io.ReadAll(io.LimitReader(f, maxRuleFileBytes+1))
			_ = f.Close()
			if err != nil {
				return
			}
			wm.reads++
			text := string(b)
			if len(b) > maxRuleFileBytes {
				text = validUTF8Prefix(text, maxRuleFileBytes) + "\n[workspace rule truncated; read the source file for full details]"
			}
			entry = cachedRule{info: info, content: text}
			wm.files[path] = entry
		}
		text := title + "\n" + entry.content + "\n\n"
		remaining := maxWorkspaceContextBytes - sb.Len()
		if remaining < 80 {
			return
		}
		if len(text) > remaining {
			text = validUTF8Prefix(text, max(0, remaining-80)) + "\n[workspace context limit reached; read source files for details]\n"
		}
		sb.WriteString(text)
	}
	add("SOUL.md", "## Core Identity (SOUL)")
	add("TEAM.md", "## Team Context")
	if entries, err := os.ReadDir(filepath.Join(wm.rootDir, ".cursor", "rules")); err == nil {
		count := 0
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".mdc") {
				if count >= 64 {
					break
				}
				count++
				add(filepath.Join(".cursor", "rules", e.Name()), fmt.Sprintf("## Development Rule: %s", e.Name()))
			}
		}
	}
	for path := range wm.files {
		if !seen[path] {
			delete(wm.files, path)
		}
	}
	return sb.String()
}
func validUTF8Prefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

type workspaceContextKey struct{}

func ContextFromContext(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(workspaceContextKey{}).(string)
	return s, ok
}

type ContextMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	manager *WorkspaceManager
}

func NewContextMiddleware(rootDir string) *ContextMiddleware {
	return &ContextMiddleware{manager: NewWorkspaceManager(rootDir)}
}

// Keep workspace rules in a replaceable message, never append into the base prompt.
func (m *ContextMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	text := m.manager.BuildContext()
	out := make([]*schema.Message, 0, len(state.Messages)+1)
	for _, msg := range state.Messages {
		if msg != nil && msg.Extra != nil && msg.Extra[ExtraWorkspaceContext] == true {
			continue
		}
		out = append(out, msg)
	}
	if text != "" {
		msg := schema.SystemMessage("[Workspace Context]\n" + text)
		msg.Extra = map[string]any{ExtraWorkspaceContext: true, "_eino_adk_runtime_generated_system_message": true}
		out = append([]*schema.Message{msg}, out...)
	}
	state.Messages = out
	return context.WithValue(ctx, workspaceContextKey{}, text), state, nil
}
