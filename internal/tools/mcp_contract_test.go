package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agentgo/internal/checkpoint"
	agentdb "agentgo/internal/db"
	"agentgo/internal/governance"
	"agentgo/internal/processutil"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/mcp"
)

type mcpPeerConfig struct {
	Label              string
	Tools              []string
	ReadOnly           bool
	EffectFile         string
	CallReady          string
	CallGate           string
	ListReady          string
	ListGate           string
	PageSize           int
	RepeatCursor       bool
	DefaultDestructive bool
	Stubborn           bool
	ChildReady         string
	StderrBytes        int
}

// A real stdio JSON-RPC peer in this test executable: no Python/npm/install,
// remote services, user MCP configuration or credentials are needed.
func TestMCPContractPeer(t *testing.T) {
	if os.Getenv("AGENTGO_MCP_CONTRACT_PEER") != "1" {
		t.Skip("subprocess fixture")
	}
	var cfg mcpPeerConfig
	raw, err := os.ReadFile(os.Args[len(os.Args)-1])
	if err != nil {
		os.Exit(2)
	}
	if json.Unmarshal(raw, &cfg) != nil {
		os.Exit(2)
	}
	if cfg.StderrBytes > 0 {
		_, _ = os.Stderr.Write([]byte(strings.Repeat("x", cfg.StderrBytes)))
	}
	if cfg.ChildReady != "" {
		executable, _ := os.Executable()
		child := exec.Command(executable, "-test.run=^TestMCPContractChild$", "--", cfg.ChildReady)
		child.Env = append(os.Environ(), "AGENTGO_MCP_CONTRACT_CHILD=1")
		processutil.HideWindow(child)
		if child.Start() != nil {
			os.Exit(4)
		}
	}
	var output sync.Mutex
	reply := func(id json.RawMessage, result any) {
		output.Lock()
		defer output.Unlock()
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	wait := func(ready, gate string) {
		if ready != "" {
			_ = os.WriteFile(ready, []byte("ready"), 0600)
		}
		if gate != "" {
			for i := 0; i < 500; i++ {
				if _, err := os.Stat(gate); err == nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			os.Exit(3)
		}
	}
	decoder := json.NewDecoder(os.Stdin)
	for {
		var request struct {
			ID     json.RawMessage
			Method string
			Params json.RawMessage
		}
		if decoder.Decode(&request) != nil {
			if cfg.Stubborn {
				for {
					time.Sleep(time.Hour)
				}
			}
			os.Exit(0)
		}
		if len(request.ID) == 0 {
			continue
		}
		switch request.Method {
		case "initialize":
			reply(request.ID, map[string]any{"protocolVersion": mcp.LATEST_PROTOCOL_VERSION, "serverInfo": map[string]string{"name": cfg.Label, "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}})
		case "tools/list":
			wait(cfg.ListReady, cfg.ListGate)
			var params struct{ Cursor string }
			_ = json.Unmarshal(request.Params, &params)
			start, _ := strconv.Atoi(params.Cursor)
			end := len(cfg.Tools)
			if cfg.PageSize > 0 && start+cfg.PageSize < end {
				end = start + cfg.PageSize
			}
			list := []mcp.Tool{}
			for _, name := range cfg.Tools[start:end] {
				opts := []mcp.ToolOption{mcp.WithDescription(cfg.Label), mcp.WithReadOnlyHintAnnotation(cfg.ReadOnly)}
				if !cfg.DefaultDestructive {
					opts = append(opts, mcp.WithDestructiveHintAnnotation(!cfg.ReadOnly))
				}
				list = append(list, mcp.NewTool(name, opts...))
			}
			result := map[string]any{"tools": list}
			if end < len(cfg.Tools) {
				result["nextCursor"] = strconv.Itoa(end)
			}
			if cfg.RepeatCursor {
				result["nextCursor"] = "same-cursor"
			}
			reply(request.ID, result)
		case "tools/call":
			go func(id, raw json.RawMessage) {
				var params struct{ Name string }
				_ = json.Unmarshal(raw, &params)
				found := false
				for _, name := range cfg.Tools {
					if name == params.Name {
						found = true
					}
				}
				if !found {
					reply(id, mcp.NewToolResultError("unknown remote tool"))
					return
				}
				wait(cfg.CallReady, cfg.CallGate)
				text := cfg.Label + "/" + params.Name
				if cfg.EffectFile != "" {
					f, e := os.OpenFile(cfg.EffectFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
					if e == nil {
						_, _ = fmt.Fprintln(f, text)
						_ = f.Close()
					}
				}
				reply(id, mcp.NewToolResultText(text))
			}(request.ID, request.Params)
		default:
			reply(request.ID, map[string]any{})
		}
	}
}

func TestMCPContractChild(t *testing.T) {
	if os.Getenv("AGENTGO_MCP_CONTRACT_CHILD") != "1" {
		t.Skip("subprocess fixture")
	}
	_ = os.WriteFile(os.Args[len(os.Args)-1], []byte(strconv.Itoa(os.Getpid())), 0600)
	for {
		time.Sleep(time.Hour)
	}
}

func peerCommand(t *testing.T, cfg mcpPeerConfig) (string, []string, []string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "peer.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return executable, []string{"-test.run=^TestMCPContractPeer$", "--", file}, append(os.Environ(), "AGENTGO_MCP_CONTRACT_PEER=1")
}
func loadPeer(t *testing.T, r *Registry, name string, cfg mcpPeerConfig) {
	t.Helper()
	command, args, env := peerCommand(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := r.LoadMCPServerWithEnv(ctx, name, command, args, env); err != nil {
		t.Fatal(err)
	}
}
func contractRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	t.Cleanup(r.CloseMCPServers)
	return r
}
func callMCP(t *testing.T, r *Registry, name string) string {
	t.Helper()
	base, ok := r.Get(name)
	if !ok {
		t.Fatal("missing tool", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := base.(tool.InvokableTool).InvokableRun(ctx, "{}")
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func waitPeerFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("peer did not reach checkpoint", path)
}

func TestMCPNamesRouteAcrossTwoRealServicesThroughEino(t *testing.T) {
	r := contractRegistry(t)
	loadPeer(t, r, "left", mcpPeerConfig{Label: "left-peer", Tools: []string{"ping"}, ReadOnly: true})
	loadPeer(t, r, "right", mcpPeerConfig{Label: "right-peer", Tools: []string{"ping"}, ReadOnly: true})
	for _, name := range []string{"left_ping", "right_ping"} {
		base, _ := r.Get(name)
		info, err := base.Info(context.Background())
		if err != nil || info.Name != name {
			t.Fatalf("registry/schema disagree: %s %+v %v", name, info, err)
		}
		info.Name = "caller mutation"
		again, _ := base.Info(context.Background())
		if again.Name != name {
			t.Fatal("Info leaked mutable identity")
		}
	}
	node, err := compose.NewToolNode(context.Background(), &compose.ToolsNodeConfig{Tools: r.GetAllTools()})
	if err != nil {
		t.Fatal(err)
	}
	out, err := node.Invoke(context.Background(), schema.AssistantMessage("", []schema.ToolCall{
		{ID: "left", Type: "function", Function: schema.FunctionCall{Name: "left_ping", Arguments: "{}"}},
		{ID: "right", Type: "function", Function: schema.FunctionCall{Name: "right_ping", Arguments: "{}"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || !strings.Contains(out[0].Content, "left-peer/ping") || !strings.Contains(out[1].Content, "right-peer/ping") {
		t.Fatalf("wrong remote routing: %+v", out)
	}
}

func TestMCPReloadIsCompleteAndFailedReplacementKeepsOldService(t *testing.T) {
	r := contractRegistry(t)
	loadPeer(t, r, "reload", mcpPeerConfig{Label: "old", Tools: []string{"ping", "removed"}, ReadOnly: true})
	stale, _ := r.Get("reload_ping")
	if err := r.LoadMCPServer(context.Background(), "reload", filepath.Join(t.TempDir(), "missing.exe"), nil); err == nil {
		t.Fatal("bad replacement succeeded")
	}
	if !strings.Contains(callMCP(t, r, "reload_ping"), "old/ping") {
		t.Fatal("failed reload destroyed old service")
	}
	loadPeer(t, r, "reload", mcpPeerConfig{Label: "new", Tools: []string{"ping"}, ReadOnly: true})
	if _, ok := r.Get("reload_removed"); ok {
		t.Fatal("removed tool survived reload")
	}
	if _, ok := governance.ToolTraitsOf("reload_removed"); ok {
		t.Fatal("removed declaration survived reload")
	}
	if !strings.Contains(callMCP(t, r, "reload_ping"), "new/ping") {
		t.Fatal("replacement did not publish")
	}
	if _, err := stale.(tool.InvokableTool).InvokableRun(context.Background(), "{}"); err == nil {
		t.Fatal("captured retired object still executes")
	}
	summaries := r.ListMCPServers()
	summaries[0].Tools[0] = "mutated"
	if r.ListMCPServers()[0].Tools[0] != "reload_ping" {
		t.Fatal("summary leaked registry storage")
	}
}

func TestMCPUnloadDoesNotRemoveAnotherServiceOrNativeTool(t *testing.T) {
	r := contractRegistry(t)
	loadPeer(t, r, "a", mcpPeerConfig{Label: "a", Tools: []string{"ping"}, ReadOnly: true})
	loadPeer(t, r, "a_b", mcpPeerConfig{Label: "b", Tools: []string{"ping"}, ReadOnly: true})
	if r.UnloadMCPServer("a") != 1 {
		t.Fatal("wrong unload count")
	}
	if !strings.Contains(callMCP(t, r, "a_b_ping"), "b/ping") {
		t.Fatal("prefix overlap removed another service")
	}
	if _, ok := governance.ToolTraitsOf("a_ping"); ok {
		t.Fatal("unload left declaration")
	}
	native, err := utils.InferTool("owned_ping", "native", func(context.Context, struct{}) (string, error) { return "native", nil })
	if err != nil {
		t.Fatal(err)
	}
	r.AddTool(native)
	command, args, env := peerCommand(t, mcpPeerConfig{Label: "foreign", Tools: []string{"ping"}, ReadOnly: true})
	if err := r.LoadMCPServerWithEnv(context.Background(), "owned", command, args, env); err == nil {
		t.Fatal("MCP overwrote native tool")
	}
	if r.UnloadMCPServer("owned") != 0 {
		t.Fatal("unload removed native tool")
	}
	current, _ := r.Get("owned_ping")
	if current != native {
		t.Fatal("native identity lost")
	}
}

func TestMCPReloadDrainsAdmittedCallsWithoutRedirectingThem(t *testing.T) {
	r := contractRegistry(t)
	ready, gate := filepath.Join(t.TempDir(), "ready"), filepath.Join(t.TempDir(), "gate")
	loadPeer(t, r, "drain", mcpPeerConfig{Label: "old", Tools: []string{"ping"}, ReadOnly: true, CallReady: ready, CallGate: gate})
	old, _ := r.Get("drain_ping")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan string, 1)
	go func() {
		out, err := old.(tool.InvokableTool).InvokableRun(ctx, "{}")
		if err != nil {
			done <- err.Error()
		} else {
			done <- out
		}
	}()
	waitPeerFile(t, ready)
	loadPeer(t, r, "drain", mcpPeerConfig{Label: "new", Tools: []string{"ping"}, ReadOnly: true})
	if !strings.Contains(callMCP(t, r, "drain_ping"), "new/ping") {
		t.Fatal("new service not available while draining")
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-done:
		if !strings.Contains(out, "old/ping") {
			t.Fatal("inflight call lost or redirected", out)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestMCPCloseAlsoTerminatesRetiredGenerationCalls(t *testing.T) {
	r := contractRegistry(t)
	ready, gate := filepath.Join(t.TempDir(), "ready"), filepath.Join(t.TempDir(), "never-open")
	loadPeer(t, r, "retired", mcpPeerConfig{Label: "old", Tools: []string{"ping"}, ReadOnly: true, CallReady: ready, CallGate: gate})
	old, _ := r.Get("retired_ping")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := old.(tool.InvokableTool).InvokableRun(ctx, "{}"); done <- err }()
	waitPeerFile(t, ready)
	loadPeer(t, r, "retired", mcpPeerConfig{Label: "new", Tools: []string{"ping"}, ReadOnly: true})
	r.CloseMCPServers()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("retired call unexpectedly completed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("retired transport survived shutdown")
	}
	r.mu.RLock()
	remaining := len(r.mcpBindings)
	r.mu.RUnlock()
	if remaining != 0 {
		t.Fatal("retired binding leaked", remaining)
	}
}

func TestMCPUnloadInvalidatesAnInProgressLoad(t *testing.T) {
	r := contractRegistry(t)
	ready, gate := filepath.Join(t.TempDir(), "ready"), filepath.Join(t.TempDir(), "gate")
	command, args, env := peerCommand(t, mcpPeerConfig{Label: "late", Tools: []string{"ping"}, ReadOnly: true, ListReady: ready, ListGate: gate})
	done := make(chan error, 1)
	go func() { done <- r.LoadMCPServerWithEnv(context.Background(), "late", command, args, env) }()
	waitPeerFile(t, ready)
	r.UnloadMCPServer("late")
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("late load resurrected removed service")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("load stuck")
	}
	if _, ok := r.Get("late_ping"); ok {
		t.Fatal("late tool published")
	}
}

func TestMCPCatalogPaginationAndBoundedProviderNames(t *testing.T) {
	r := contractRegistry(t)
	long := "remote/" + strings.Repeat("long", 25)
	loadPeer(t, r, "pages", mcpPeerConfig{Label: "pages", Tools: []string{"one", "two", long}, ReadOnly: true, PageSize: 1})
	for _, remote := range []string{"one", "two", long} {
		name := mcpModelName("pages", remote)
		if !ValidToolName(name) || !strings.Contains(callMCP(t, r, name), "pages/") {
			t.Fatal("invalid model alias", name)
		}
	}
	command, args, env := peerCommand(t, mcpPeerConfig{Tools: []string{"ping"}, RepeatCursor: true})
	if err := r.LoadMCPServerWithEnv(context.Background(), "badpages", command, args, env); err == nil {
		t.Fatal("repeated cursor accepted")
	}
}

func TestMCPVerbosePeerDoesNotBlockOnStderr(t *testing.T) {
	r := contractRegistry(t)
	loadPeer(t, r, "verbose", mcpPeerConfig{Label: "verbose", Tools: []string{"ping"}, ReadOnly: true, StderrBytes: 2 << 20})
	if !strings.Contains(callMCP(t, r, "verbose_ping"), "verbose/ping") {
		t.Fatal("verbose peer did not respond")
	}
}

func TestMCPMissingOrReplacedDeclarationCannotDowngradeExecution(t *testing.T) {
	r := contractRegistry(t)
	effects := filepath.Join(t.TempDir(), "effects")
	loadPeer(t, r, "shadow", mcpPeerConfig{Label: "remote-write", Tools: []string{"ping"}, EffectFile: effects})
	remove := governance.RegisterToolTraits("shadow_ping", governance.ToolTraits{Source: "other-provider", Risk: governance.RiskLow})
	defer remove()
	q, err := governance.NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	base, _ := r.Get("shadow_ping")
	_, err = governance.NewGovernanceMiddleware(q, governance.DefaultPolicy()).InvokeWithPolicy(context.Background(), "shadow_ping", "{}", func(ctx context.Context, args string) (string, error) {
		return base.(tool.InvokableTool).InvokableRun(ctx, args)
	})
	if err == nil || !strings.Contains(err.Error(), "definition changed") {
		t.Fatal("unversioned declaration authorized remote tool", err)
	}
	if _, err := os.Stat(effects); !os.IsNotExist(err) {
		t.Fatal("remote tool executed after losing its declaration")
	}
}

func TestMCPManagerReportsRuntimeSuccessWithPersistenceFailure(t *testing.T) {
	r := contractRegistry(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "mcp_servers.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := RegisterMCPManagerTool(r, root); err != nil {
		t.Fatal(err)
	}
	command, args, _ := peerCommand(t, mcpPeerConfig{Label: "managed", Tools: []string{"ping"}, ReadOnly: true})
	base, _ := r.Get("manage_mcp_server")
	raw, err := json.Marshal(mcpManagerInput{Action: "register", Name: "managed", Command: command, Args: args, Env: map[string]string{"AGENTGO_MCP_CONTRACT_PEER": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := base.(tool.InvokableTool).InvokableRun(context.Background(), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	var result mcpManagerOutput
	if err = json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(result.Message, "service is loaded") {
		t.Fatal("persistence failure hidden", out)
	}
	if !strings.Contains(callMCP(t, r, "managed_ping"), "managed/ping") {
		t.Fatal("actual runtime outcome was not preserved")
	}
	persist := false
	raw, _ = json.Marshal(mcpManagerInput{Action: "register", Name: "managed", Command: filepath.Join(t.TempDir(), "missing.exe"), Persist: &persist})
	out, err = base.(tool.InvokableTool).InvokableRun(context.Background(), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(out), &result); err != nil || result.Success {
		t.Fatal("failed manager replacement reported success", out, err)
	}
	if !strings.Contains(callMCP(t, r, "managed_ping"), "managed/ping") {
		t.Fatal("manager destroyed previous service on failure")
	}
}

func TestMCPConfigUpdatePreservesMalformedFileAndReplacesValidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp_servers.json")
	original := []byte("{broken config")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := upsertMCPConfigFile(path, "x", MCPServerConfig{Command: "example"}); err == nil {
		t.Fatal("malformed config silently replaced")
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != string(original) {
		t.Fatal("malformed config was modified")
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := upsertMCPConfigFile(path, "x", MCPServerConfig{Command: "example"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadMCPConfigFile(path)
	if err != nil || cfg.MCPServers["x"].Command != "example" {
		t.Fatal("atomic replacement lost entry", cfg, err)
	}
	if err := removeMCPConfigEntry(path, "x"); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadMCPConfigFile(path)
	if err != nil || len(cfg.MCPServers) != 0 {
		t.Fatal("remove not persisted", cfg, err)
	}
}

func mcpStateDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err = agentdb.Configure(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func TestMCPRiskCannotDropAfterReloadUnloadOrDatabaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := mcpStateDB(t, path)
	r := contractRegistry(t)
	if err := r.SetMCPStateDB(db); err != nil {
		t.Fatal(err)
	}
	loadPeer(t, r, "risk", mcpPeerConfig{Label: "read", Tools: []string{"ping"}, ReadOnly: true, DefaultDestructive: true})
	if governance.StaticRisk("risk_ping") != governance.RiskLow {
		t.Fatal("explicit read-only not recognized")
	}
	loadPeer(t, r, "risk", mcpPeerConfig{Label: "write", Tools: []string{"ping"}})
	if governance.StaticRisk("risk_ping") != governance.RiskHigh {
		t.Fatal("unknown/write not governed")
	}
	r.UnloadMCPServer("risk")
	loadPeer(t, r, "risk", mcpPeerConfig{Label: "claims-read", Tools: []string{"ping"}, ReadOnly: true})
	if governance.StaticRisk("risk_ping") != governance.RiskHigh {
		t.Fatal("declaration downgraded observed risk")
	}
	r.CloseMCPServers()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mcpStateDB(t, path)
	reopened := contractRegistry(t)
	if err := reopened.SetMCPStateDB(db); err != nil {
		t.Fatal(err)
	}
	loadPeer(t, reopened, "risk", mcpPeerConfig{Label: "after-restart", Tools: []string{"ping"}, ReadOnly: true})
	if governance.StaticRisk("risk_ping") != governance.RiskHigh {
		t.Fatal("restart reset risk floor")
	}
}

type mcpApprovalModel struct{ name string }

func (m mcpApprovalModel) Generate(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	for _, msg := range msgs {
		if msg.Role == schema.Tool {
			return schema.AssistantMessage("done", nil), nil
		}
	}
	return schema.AssistantMessage("", []schema.ToolCall{{ID: "mcp-call", Type: "function", Function: schema.FunctionCall{Name: m.name, Arguments: "{}"}}}), nil
}
func (m mcpApprovalModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	out, err := m.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{out}), nil
}
func (m mcpApprovalModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func TestMCPCheckpointApprovalCannotExecuteReloadedDefinition(t *testing.T) {
	testMCPCheckpointApproval(t, true)
}

func TestMCPCheckpointApprovalExecutesUnchangedDefinitionOnce(t *testing.T) {
	testMCPCheckpointApproval(t, false)
}

func testMCPCheckpointApproval(t *testing.T, reload bool) {
	r := contractRegistry(t)
	db := mcpStateDB(t, filepath.Join(t.TempDir(), "approval.db"))
	if err := r.SetMCPStateDB(db); err != nil {
		t.Fatal(err)
	}
	q, err := governance.NewApprovalQueueWithDB(db)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := checkpoint.OpenSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	effects := filepath.Join(t.TempDir(), "effects")
	loadPeer(t, r, "approve", mcpPeerConfig{Label: "old", Tools: []string{"ping"}, EffectFile: effects})
	ctx := governance.WithExecution(governance.WithSessionID(context.Background(), "mcp-session"))
	build := func(ctx context.Context) *adk.Runner {
		a, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "mcp-test", Description: "test", Model: mcpApprovalModel{name: "approve_ping"}, Handlers: []adk.ChatModelAgentMiddleware{governance.NewGovernanceMiddleware(q, governance.DefaultPolicy())}, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: r.GetAllTools()}}, MaxIterations: 3})
		if err != nil {
			t.Fatal(err)
		}
		return adk.NewRunner(ctx, adk.RunnerConfig{Agent: a, CheckPointStore: cp})
	}
	it := build(ctx).Query(ctx, "execute", adk.WithCheckPointID("cp"))
	interrupt := ""
	for {
		e, ok := it.Next()
		if !ok {
			break
		}
		if e.Err != nil {
			t.Fatal(e.Err)
		}
		if e.Action != nil && e.Action.Interrupted != nil {
			for _, ic := range e.Action.Interrupted.InterruptContexts {
				if ic.IsRootCause {
					interrupt = ic.ID
				}
			}
		}
	}
	requests, _ := q.ListPending(ctx, nil)
	if len(requests) != 1 || interrupt == "" {
		t.Fatal("unknown MCP tool bypassed approval", requests)
	}
	if _, err := os.Stat(effects); !os.IsNotExist(err) {
		t.Fatal("tool executed before approval")
	}
	if err := q.Resolve(ctx, requests[0].ID, true, "ok", "test", &governance.ResumePayload{Approved: true}); err != nil {
		t.Fatal(err)
	}
	if reload {
		loadPeer(t, r, "approve", mcpPeerConfig{Label: "new", Tools: []string{"ping"}, EffectFile: effects})
	}
	resumeCtx := governance.WithExecution(governance.WithSessionID(context.Background(), "mcp-session"))
	it, err = build(resumeCtx).ResumeWithParams(resumeCtx, "cp", &adk.ResumeParams{Targets: map[string]any{interrupt: &governance.ResumePayload{Approved: true}}})
	if err != nil {
		t.Fatal(err)
	}
	var failures strings.Builder
	for {
		e, ok := it.Next()
		if !ok {
			break
		}
		if e.Err != nil {
			failures.WriteString(e.Err.Error())
		}
	}
	if !reload {
		if failures.Len() != 0 {
			t.Fatal("unchanged definition failed to resume", failures.String())
		}
		data, err := os.ReadFile(effects)
		if err != nil || strings.Count(string(data), "old/ping") != 1 {
			t.Fatalf("approved action missing/repeated: %q %v", data, err)
		}
		request, err := q.GetRequest(ctx, requests[0].ID)
		if err != nil || request.ConsumedAt == nil {
			t.Fatal("approval not consumed", request, err)
		}
		return
	}
	if !strings.Contains(failures.String(), "definition changed") {
		t.Fatal("stale approval was not explicitly refused", failures.String())
	}
	if _, err := os.Stat(effects); !os.IsNotExist(err) {
		t.Fatal("new definition executed using old approval")
	}
	request, err := q.GetRequest(ctx, requests[0].ID)
	if err != nil || request.ConsumedAt != nil {
		t.Fatal("stale approval consumed", request, err)
	}
}
