package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agentgo/internal/governance"
	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// Feed the exact catalog snapshot to Eino's existing schema/result adapter.
// Listing metadata and building tools cannot observe different definitions.
type mcpCatalogClient struct {
	client.MCPClient
	catalog *mcp.ListToolsResult
}

type mcpLoad struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (c *mcpCatalogClient) ListTools(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return c.catalog, nil
}

func readMCPCatalog(ctx context.Context, c *client.Client) (*mcp.ListToolsResult, error) {
	out := &mcp.ListToolsResult{}
	seen := map[mcp.Cursor]bool{}
	req := mcp.ListToolsRequest{}
	for page := 0; page < 32; page++ {
		result, err := c.ListToolsByPage(ctx, req)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, fmt.Errorf("mcp: empty catalog")
		}
		out.Tools = append(out.Tools, result.Tools...)
		if len(out.Tools) > 1024 {
			return nil, fmt.Errorf("mcp: catalog exceeds 1024 tools")
		}
		if result.NextCursor == "" {
			return out, nil
		}
		if seen[result.NextCursor] {
			return nil, fmt.Errorf("mcp: repeated catalog cursor")
		}
		seen[result.NextCursor] = true
		req.Params.Cursor = result.NextCursor
	}
	return nil, fmt.Errorf("mcp: catalog exceeds 32 pages")
}

func (r *Registry) loadMCP(ctx context.Context, server string, target mcpTarget) error {
	server = strings.TrimSpace(server)
	if !mcpServerNameRe.MatchString(server) || (strings.TrimSpace(target.Command) == "" && !target.remote()) {
		return fmt.Errorf("mcp: valid server name and command or url required")
	}
	r.mu.Lock()
	if r.mcpClosed {
		r.mu.Unlock()
		return fmt.Errorf("mcp: registry closed")
	}
	if pending := r.mcpLoads[server]; pending != nil {
		pending.cancel()
	}
	ctx, cancel := context.WithCancel(ctx)
	attempt := &mcpLoad{cancel: cancel, done: make(chan struct{})}
	r.mcpLoads[server] = attempt
	r.mcpStarted = true
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		if r.mcpLoads[server] == attempt {
			delete(r.mcpLoads, server)
		}
		r.mu.Unlock()
		close(attempt.done)
	}()
	c, closer, via, err := connectMCP(ctx, target)
	if err != nil {
		return fmt.Errorf("mcp: connect %s: %w", server, err)
	}
	installed := false
	defer func() {
		if !installed {
			_ = closer.Close()
		}
	}()
	catalog, err := readMCPCatalog(ctx, c)
	if err != nil {
		return fmt.Errorf("mcp: list %s: %w", server, err)
	}
	adapter, err := einomcp.GetTools(ctx, &einomcp.Config{Cli: &mcpCatalogClient{MCPClient: c, catalog: catalog}})
	if err != nil {
		return fmt.Errorf("mcp: adapt %s: %w", server, err)
	}
	if len(adapter) != len(catalog.Tools) {
		// Tools are paired with their declarations by position.
		return fmt.Errorf("mcp: %s declares %d tools but %d were built", server, len(catalog.Tools), len(adapter))
	}
	life, abort := context.WithCancel(context.Background())
	binding := &mcpBinding{closer: closer, closed: make(chan struct{}), life: life, abort: abort}
	binding.onClosed = func() { r.mu.Lock(); delete(r.mcpBindings, binding); r.mu.Unlock() }
	generation := uuid.NewString()
	prepared := make(map[string]*boundMCPTool, len(adapter))
	risks := make(map[string]int, len(adapter))
	for i, raw := range adapter {
		info, e := raw.Info(ctx)
		invokable, ok := raw.(tool.InvokableTool)
		if e != nil || info == nil || !ok || info.Name == "" {
			return fmt.Errorf("mcp: invalid tool in %s", server)
		}
		decl := catalog.Tools[i]
		name := mcpModelName(server, info.Name)
		if _, duplicate := prepared[name]; duplicate {
			return fmt.Errorf("mcp: duplicate tool name %q", name)
		}
		encoded, e := json.Marshal(decl)
		if e != nil {
			return e
		}
		h := sha256.Sum256(append([]byte(generation+"\x00"), encoded...))
		definition := hex.EncodeToString(h[:])
		copyInfo := *info
		copyInfo.Name = name
		copyInfo.Extra = map[string]any{"mcp_server": server, "mcp_remote_name": info.Name, "tool_definition": definition}
		prepared[name] = &boundMCPTool{inner: invokable, info: copyInfo, binding: binding, definition: definition}
		risk := 1
		if decl.Annotations.ReadOnlyHint != nil && *decl.Annotations.ReadOnlyHint {
			risk = 0
		}
		risks[info.Name] = risk
	}
	r.mu.Lock()
	if r.mcpClosed || r.mcpLoads[server] != attempt {
		r.mu.Unlock()
		return fmt.Errorf("mcp: load superseded or registry closed")
	}
	old := r.mcpServers[server]
	for name := range prepared {
		if current, exists := r.tools[name]; exists {
			bound, owned := current.(*boundMCPTool)
			if !owned || old == nil || bound.binding != old.binding {
				r.mu.Unlock()
				return fmt.Errorf("mcp: tool name %q already owned", name)
			}
		}
	}
	for name, risk := range risks {
		if floor := r.mcpRisks[server+"\x00"+name]; floor > risk {
			risks[name] = floor
		}
	}
	if r.mcpState != nil {
		if err = r.mcpState.observe(ctx, server, risks); err != nil {
			r.mu.Unlock()
			return fmt.Errorf("mcp: persist risk policy: %w", err)
		}
	}
	if err = ctx.Err(); err != nil {
		r.mu.Unlock()
		return err
	}
	var retire func()
	if old != nil {
		r.removeMCPLocked(old)
		retire = old.binding.retire(false)
	}
	summary := &MCPServerSummary{Name: server, Command: target.Command, Transport: via, binding: binding}
	if target.remote() {
		// Show where it lives, never the credentials that may be in the address.
		resolved, _, _ := resolveMCPRemote(target.URL, nil)
		summary.URL = redactMCPURL(resolved)
	}
	for name, t := range prepared {
		remote, _ := t.info.Extra["mcp_remote_name"].(string)
		risk := governance.RiskLow
		if risks[remote] > 0 || r.mcpState == nil {
			risk = governance.RiskHigh
		}
		r.mcpRisks[server+"\x00"+remote] = risks[remote]
		binding.dispose = append(binding.dispose, Declare(Spec{Name: name, Source: "mcp:" + server, Risk: risk, Mutating: risk == governance.RiskHigh, DefinitionID: t.definition}))
		r.tools[name] = t
		summary.Tools = append(summary.Tools, name)
	}
	sort.Strings(summary.Tools)
	r.mcpServers[server] = summary
	r.mcpBindings[binding] = struct{}{}
	installed = true
	r.mu.Unlock()
	if retire != nil {
		retire()
	}
	return nil
}

func (r *Registry) removeMCPLocked(s *MCPServerSummary) int {
	n := 0
	for _, name := range s.Tools {
		if t, ok := r.tools[name].(*boundMCPTool); ok && t.binding == s.binding {
			delete(r.tools, name)
			n++
		}
	}
	delete(r.mcpServers, s.Name)
	return n
}
func (r *Registry) unloadMCP(server string) int {
	r.mu.Lock()
	pending := r.mcpLoads[server]
	if pending != nil {
		pending.cancel()
		delete(r.mcpLoads, server)
	}
	var retire func()
	n := 0
	if s := r.mcpServers[server]; s != nil {
		n = r.removeMCPLocked(s)
		retire = s.binding.retire(false)
	}
	r.mu.Unlock()
	if retire != nil {
		retire()
	}
	if pending != nil {
		<-pending.done
	}
	return n
}

// Runtime shutdown invalidates captured tools and closes active transports.
func (r *Registry) CloseMCPServers() {
	r.mu.Lock()
	r.mcpClosed = true
	var pending []*mcpLoad
	for name, load := range r.mcpLoads {
		load.cancel()
		pending = append(pending, load)
		delete(r.mcpLoads, name)
	}
	var retired []func()
	for _, s := range r.mcpServers {
		r.removeMCPLocked(s)
	}
	var closed []<-chan struct{}
	for binding := range r.mcpBindings {
		retired = append(retired, binding.retire(true))
		closed = append(closed, binding.closed)
	}
	r.mu.Unlock()
	for _, finish := range retired {
		finish()
	}
	for _, load := range pending {
		<-load.done
	}
	for _, done := range closed {
		<-done
	}
}
