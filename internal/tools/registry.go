package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// Registry manages the tools available to the AgentGo runtime.
type Registry struct {
	mu              sync.RWMutex
	tools           map[string]einotool.BaseTool
	dynamicCompiled map[string]bool
	dynamicSandbox  string
}

// NewRegistry creates a new empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:           make(map[string]einotool.BaseTool),
		dynamicCompiled: make(map[string]bool),
	}
}

// AddTool registers a single Eino BaseTool.
func (r *Registry) AddTool(t einotool.BaseTool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	info, err := t.Info(context.Background())
	if err == nil && info != nil {
		r.tools[info.Name] = t
	}
}

// LoadMCPServer connects to an MCP server (e.g. over stdio) and registers all its tools.
func (r *Registry) LoadMCPServer(ctx context.Context, serverName, command string, args []string) error {
	// Initialize MCP client
	mcpClient, err := client.NewStdioMCPClient(command, nil, args...)
	if err != nil {
		return fmt.Errorf("failed to create MCP client for %s: %w", serverName, err)
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "agentgo",
		Version: "1.0.0",
	}

	_, err = mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		return fmt.Errorf("failed to initialize MCP client for %s: %w", serverName, err)
	}

	cfg := &einomcp.Config{
		Cli: mcpClient,
	}

	mcpTools, err := einomcp.GetTools(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to get tools from MCP server %s: %w", serverName, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, t := range mcpTools {
		info, err := t.Info(ctx)
		if err == nil {
			// Prefix the tool name with the server name
			prefixedName := fmt.Sprintf("%s_%s", serverName, info.Name)
			r.tools[prefixedName] = t
		}
	}

	return nil
}

// Get returns a registered tool by name.
func (r *Registry) Get(name string) (einotool.BaseTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[strings.TrimSpace(name)]
	return t, ok
}

// GetAllTools returns all currently registered tools.
func (r *Registry) GetAllTools() []einotool.BaseTool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []einotool.BaseTool
	for _, t := range r.tools {
		list = append(list, t)
	}
	return list
}

// RemoveTool deletes a tool by its name from the registry.
func (r *Registry) RemoveTool(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// RemoveToolsWithPrefix deletes all tools whose names start with the given prefix.
func (r *Registry) RemoveToolsWithPrefix(prefix string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name := range r.tools {
		if strings.HasPrefix(name, prefix) {
			delete(r.tools, name)
		}
	}
}
