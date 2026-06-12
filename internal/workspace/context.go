package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// WorkspaceManager reads local documentation and rules to build the context.
type WorkspaceManager struct {
	rootDir string
}

// NewWorkspaceManager creates a manager for the given root directory.
func NewWorkspaceManager(rootDir string) *WorkspaceManager {
	return &WorkspaceManager{rootDir: rootDir}
}

// BuildContext collects content from standard agent files like SOUL.md, TEAM.md, and .cursor/rules.
func (wm *WorkspaceManager) BuildContext() string {
	var sb strings.Builder

	// Read SOUL.md
	if b, err := os.ReadFile(filepath.Join(wm.rootDir, "SOUL.md")); err == nil {
		sb.WriteString("## Core Identity (SOUL)\n")
		sb.Write(b)
		sb.WriteString("\n\n")
	}

	// Read TEAM.md
	if b, err := os.ReadFile(filepath.Join(wm.rootDir, "TEAM.md")); err == nil {
		sb.WriteString("## Team Context\n")
		sb.Write(b)
		sb.WriteString("\n\n")
	}

	// Read .cursor/rules
	rulesDir := filepath.Join(wm.rootDir, ".cursor", "rules")
	if entries, err := os.ReadDir(rulesDir); err == nil {
		sb.WriteString("## Development Rules\n")
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".mdc") {
				if b, err := os.ReadFile(filepath.Join(rulesDir, e.Name())); err == nil {
					sb.WriteString(fmt.Sprintf("### %s\n", e.Name()))
					sb.Write(b)
					sb.WriteString("\n\n")
				}
			}
		}
	}

	return sb.String()
}

// ContextMiddleware is an Eino ADK Middleware that injects workspace context into the system prompt.
type ContextMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	manager *WorkspaceManager
}

// NewContextMiddleware creates the workspace context injection middleware.
func NewContextMiddleware(rootDir string) *ContextMiddleware {
	return &ContextMiddleware{
		manager: NewWorkspaceManager(rootDir),
	}
}

// BeforeModelRewriteState injects the workspace markdown into the system prompt.
func (m *ContextMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	wsContext := m.manager.BuildContext()
	if wsContext != "" {
		hasSystem := false
		for i, msg := range state.Messages {
			if msg.Role == schema.System {
				// Inject workspace context at the end of the existing system message
				state.Messages[i].Content = fmt.Sprintf("%s\n\n[Workspace Context]:\n%s", msg.Content, wsContext)
				hasSystem = true
				break
			}
		}

		if !hasSystem {
			sysMsg := &schema.Message{
				Role:    schema.System,
				Content: fmt.Sprintf("[Workspace Context]:\n%s", wsContext),
			}
			state.Messages = append([]*schema.Message{sysMsg}, state.Messages...)
		}
	}
	return ctx, state, nil
}
