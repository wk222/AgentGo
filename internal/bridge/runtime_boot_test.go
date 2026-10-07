package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/backgroundtask"
	backgroundshell "github.com/cloudwego/eino/adk/backgroundtask/shell"
	einotool "github.com/cloudwego/eino/components/tool"

	"agentgo/internal/memory"
	"agentgo/internal/plugin"
)

// bootIn assembles a runtime in dir with a private workspace and no network
// listener, and closes it when the test ends.
func bootIn(t *testing.T, dir string, adjust func([]plugin.Plugin) []plugin.Plugin) *Runtime {
	t.Helper()
	t.Setenv("AGENTGO_WORKSPACE_ROOT", t.TempDir())
	t.Setenv("AGENTGO_GATEWAY_ADDR", "")
	t.Setenv("AGENTGO_GATEWAY_PORT", "")
	t.Setenv(pluginsDisabledEnv, "")
	rt, err := newRuntimeWith(dir, adjust)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func boot(t *testing.T) *Runtime { t.Helper(); return bootIn(t, t.TempDir(), nil) }

func statusMap(rt *Runtime) map[string]plugin.Status {
	m := map[string]plugin.Status{}
	for _, st := range rt.host.Statuses() {
		m[st.Name] = st
	}
	return m
}

var coreRuntimePlugins = []string{
	"storage", "model", "memory", "sessions", "checkpoints", "approvals", "capability", "workspace",
	"tools", "agent", "workflow", "apps", "toolset", "kanban", "taskhub", "scheduler",
}

var optionalRuntimePlugins = []string{"gateway", "distill", "capability-sync", "ide"}

func TestRuntimeIsAssembledByThePluginHost(t *testing.T) {
	rt := boot(t)
	sts := statusMap(rt)

	for _, name := range coreRuntimePlugins {
		st, ok := sts[name]
		if !ok || st.State != plugin.StateRunning || !st.Protected {
			t.Errorf("%s: want running+protected, got %+v (present=%v)", name, st, ok)
		}
	}
	for _, name := range optionalRuntimePlugins {
		st, ok := sts[name]
		if !ok || st.State != plugin.StateRunning || st.Protected {
			t.Errorf("%s: want running and not protected, got %+v (present=%v)", name, st, ok)
		}
	}
	// What the rest of the bridge reads is filled in by the plugins.
	for name, ok := range map[string]bool{
		"sessions": rt.sessions != nil, "approvals": rt.approvals != nil, "pending": rt.pending != nil,
		"agent": rt.agentRunner != nil, "tools": rt.toolReg != nil, "capability": rt.capBus != nil,
		"workflow": rt.wfStore != nil, "taskhub": rt.taskHub != nil, "kanban": rt.kanban != nil,
		"scheduler": rt.sched != nil, "memory": rt.mem != nil, "checkpoints": rt.cpStore != nil,
		"workspace": rt.wsMiddleware != nil && rt.workspace != "", "apps": rt.appStore != nil,
		"adminRunner": rt.adminRunner != nil, "dynStore": rt.dynStore != nil,
	} {
		if !ok {
			t.Errorf("runtime field for %s was not set", name)
		}
	}
	// Every service is published under its name.
	for _, svc := range []string{svcDB, svcModel, svcMemory, svcSessions, svcApprovals, svcCheckpoints, svcWorkspace,
		svcCapability, svcTools, svcAgent, svcWorkflow, svcApps, svcToolset, svcTaskHub, svcIDE} {
		if _, ok := rt.host.Service(svc); !ok {
			t.Errorf("service %q is not available", svc)
		}
	}
	// The built-in tools were registered through the toolset plugin.
	for _, name := range []string{"remember", "recall_memories"} {
		if _, ok := rt.toolReg.Get(name); !ok {
			t.Errorf("tool %q was not registered", name)
		}
	}
}

func TestRuntimeCloseStopsDependentsBeforeTheirProviders(t *testing.T) {
	var stopped []string
	rt := bootIn(t, t.TempDir(), func(ps []plugin.Plugin) []plugin.Plugin {
		out := make([]plugin.Plugin, len(ps))
		for i, p := range ps {
			p := p
			apply := p.Apply
			p.Apply = func(c *plugin.Context) error {
				if err := apply(c); err != nil {
					return err
				}
				// Registered last, so it runs first when this plugin stops.
				c.Effect("order", func() { stopped = append(stopped, c.Name()) })
				return nil
			}
			out[i] = p
		}
		return out
	})
	graph := rt.host.Graph()
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, n := range stopped {
		pos[n] = i
	}
	if len(pos) < len(coreRuntimePlugins) {
		t.Fatalf("only %d plugins reported stopping: %v", len(pos), stopped)
	}
	checked := 0
	for _, e := range graph.Edges {
		from, okF := pos[e.From]
		to, okT := pos[e.To]
		if !okF || !okT {
			continue
		}
		checked++
		if from > to {
			t.Errorf("%s (needs %q) stopped after %s, which provides it\norder: %v", e.From, e.Service, e.To, stopped)
		}
	}
	if checked < 20 {
		t.Fatalf("only %d dependency edges were checked; the graph looks too small: %+v", checked, graph.Edges)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// goroutineSet returns the ids of the live goroutines.
func goroutineSet() map[string]string {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	out := map[string]string{}
	for _, block := range strings.Split(string(buf), "\n\n") {
		fields := strings.Fields(block)
		if len(fields) >= 2 && fields[0] == "goroutine" {
			out[fields[1]] = block
		}
	}
	return out
}

// leakedSince lists goroutines that appeared after before and still run agentgo
// code. Goroutines started by test functions themselves are not leaks.
func leakedSince(before map[string]string) []string {
	var leaks []string
	for id, block := range goroutineSet() {
		if _, old := before[id]; old {
			continue
		}
		if !strings.Contains(block, "agentgo/internal/") {
			continue
		}
		if strings.Contains(block, "created by agentgo/internal/bridge.Test") ||
			strings.Contains(block, "internal/bridge.leakedSince") ||
			strings.Contains(block, "testing.tRunner") {
			continue
		}
		leaks = append(leaks, block)
	}
	return leaks
}

func TestRuntimeCloseReleasesBackgroundWorkAndTheDatabase(t *testing.T) {
	before := goroutineSet()
	dir := t.TempDir()
	t.Setenv("AGENTGO_WORKSPACE_ROOT", t.TempDir())
	t.Setenv("AGENTGO_GATEWAY_ADDR", "127.0.0.1:0") // run the gateway too
	t.Setenv(pluginsDisabledEnv, "")
	rt, err := newRuntimeAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st := statusMap(rt)["gateway"]; st.State != plugin.StateRunning || rt.gatewaySrv == nil {
		t.Fatalf("gateway did not start: %+v", st)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Sessions().Create(context.Background(), "after close"); err == nil {
		t.Fatal("the database must be closed after Close")
	}

	var leaks []string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if leaks = leakedSince(before); len(leaks) == 0 {
			return
		}
	}
	for _, l := range leaks {
		t.Errorf("goroutine still running after Close:\n%s", l)
	}
}

// A command the agent left running in the background has no owner once the
// process is gone (task state is in memory), so closing the runtime ends it.
func TestRuntimeCloseEndsBackgroundCommands(t *testing.T) {
	rt := boot(t)
	coord := rt.agentRunner.BackgroundTaskCoordinator()
	if coord == nil {
		t.Fatal("agent has no background task coordinator")
	}
	command := "sleep 60"
	if runtime.GOOS == "windows" {
		command = "ping -n 60 127.0.0.1 > nul"
	}
	run, err := coord.Shell().StartCommand(context.Background(),
		&backgroundshell.StartCommandRequest{TaskID: "left-running", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("the command was still running after Close: %v", err)
	}
	if out.Status != backgroundtask.StatusCanceled || !strings.Contains(out.Error, "interrupted") {
		t.Errorf("status=%s error=%q", out.Status, out.Error)
	}
}

// The same for experiments the agent started with the job_monitor tool.
func TestRuntimeCloseEndsMonitoredJobs(t *testing.T) {
	rt := boot(t)
	command := "sleep 60"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep 60"
	}
	res, err := rt.toolReg.InvokeJSON(context.Background(), "job_monitor",
		`{"action":"start","name":"long","command":"`+command+`"}`)
	if err != nil || !strings.Contains(res, "Job ID") {
		t.Fatalf("start: %v %s", err, res)
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	list, err := rt.toolReg.InvokeJSON(context.Background(), "job_monitor", `{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(list, `"running"`) || !strings.Contains(list, `"cancelled"`) {
		t.Fatalf("the job was still running after Close: %s", list)
	}
}

func TestRuntimeCanBeReassembledInTheSameProcess(t *testing.T) {
	dir := t.TempDir()
	rt1 := bootIn(t, dir, nil)
	sess, err := rt1.Sessions().Create(context.Background(), "survives")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt1.Close(); err != nil {
		t.Fatal(err)
	}
	rt2 := bootIn(t, dir, nil)
	list, err := rt2.Sessions().List(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range list {
		found = found || s.ID == sess.ID
	}
	if !found {
		t.Fatalf("session %s did not survive a restart: %+v", sess.ID, list)
	}
}

func TestConfigurationChoosesWhichOptionalPluginsStart(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := json.Marshal(map[string]any{"plugins": map[string]any{"disabled": []string{"distill"}}})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTGO_WORKSPACE_ROOT", t.TempDir())
	t.Setenv(pluginsDisabledEnv, "ide, storage ,nonesuch") // storage is protected, the last does not exist
	rt, err := newRuntimeAt(dir)
	if err != nil {
		t.Fatalf("disabling optional plugins must not stop the runtime from starting: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	sts := statusMap(rt)
	for _, name := range []string{"ide", "distill"} {
		if st := sts[name]; st.State != plugin.StatePending || !st.Disabled {
			t.Errorf("%s should be disabled, got %+v", name, st)
		}
	}
	if st := sts["storage"]; st.State != plugin.StateRunning || st.Disabled {
		t.Errorf("a protected plugin cannot be disabled by configuration: %+v", st)
	}
	// A plugin that only optionally uses ide still starts; nothing else needed it.
	if st := sts["capability-sync"]; st.State != plugin.StateRunning {
		t.Errorf("capability-sync: %+v", st)
	}
	if rt.supervisor != nil || rt.workspaceFS != nil {
		t.Error("the IDE services must not exist when the ide plugin is disabled")
	}
	// The graph reports why: ide's service is missing, nobody hard-depends on it.
	for _, e := range rt.host.Graph().Edges {
		if e.Service == svcIDE && e.Resolved {
			t.Errorf("edge to a disabled plugin must be unresolved: %+v", e)
		}
	}
}

func TestBootFailsNamingTheProtectedPluginThatFailed(t *testing.T) {
	// A data directory that is a file: the database cannot be created in it.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTGO_WORKSPACE_ROOT", t.TempDir())
	before := goroutineSet()
	rt, err := newRuntimeAt(file)
	if err == nil {
		_ = rt.Close()
		t.Fatal("boot must fail when storage cannot start")
	}
	if !strings.Contains(err.Error(), `"storage"`) {
		t.Fatalf("the error should name the plugin that failed, got: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if leaks := leakedSince(before); len(leaks) > 0 {
		t.Errorf("a failed boot left %d goroutine(s) running:\n%s", len(leaks), strings.Join(leaks, "\n---\n"))
	}
}

func TestUsersCannotStopCorePluginsButCanRestartOptionalOnes(t *testing.T) {
	rt := boot(t)
	s := NewAppService(rt)
	t.Cleanup(s.Close)

	for _, name := range []string{"storage", "sessions", "agent", "tools"} {
		if out := s.StopPlugin(name); out["success"] != false {
			t.Fatalf("StopPlugin(%s) must be refused: %+v", name, out)
		}
		if out := s.ReloadPlugin(name); out["success"] != false {
			t.Fatalf("ReloadPlugin(%s) must be refused: %+v", name, out)
		}
	}
	if _, err := rt.Sessions().Create(context.Background(), "still works"); err != nil {
		t.Fatalf("a refused stop must leave the runtime working: %v", err)
	}

	if out := s.StopPlugin("ide"); out["success"] != true {
		t.Fatalf("stopping ide: %+v", out)
	}
	if out := s.ReloadPlugin("ide"); out["success"] != true {
		t.Fatalf("reloading ide: %+v", out)
	}
	if st := statusMap(rt)["ide"]; st.State != plugin.StateRunning {
		t.Fatalf("ide after reload: %+v", st)
	}

	// The first-party plugins joined the same host, so one graph shows everything.
	g := s.PluginGraph()
	names := map[string]bool{}
	for _, n := range g.Nodes {
		names[n.Name] = true
	}
	for _, want := range []string{"storage", "agent", "eino", "codetools"} {
		if !names[want] {
			t.Errorf("graph is missing %q: %v", want, names)
		}
	}
	for _, e := range g.Edges {
		if !e.Optional && !e.Resolved {
			if st := statusMap(rt)[e.From]; st.State == plugin.StateRunning {
				t.Errorf("a running plugin has an unresolved required edge: %+v", e)
			}
		}
	}
}

// stubMemory counts what it is asked to store; every other call is unused here.
type stubMemory struct {
	memory.Engine
	ingested []memory.Record
}

func (m *stubMemory) Ingest(_ context.Context, r memory.Record) error {
	m.ingested = append(m.ingested, r)
	return nil
}

// Replacing the memory implementation must need no change in its consumers:
// the agent and the tools get memory from the host, by name.
func TestReplacingMemoryNeedsNoChangeInItsConsumers(t *testing.T) {
	stub := &stubMemory{}
	rt := bootIn(t, t.TempDir(), func(ps []plugin.Plugin) []plugin.Plugin {
		for i, p := range ps {
			if p.Name == "memory" {
				ps[i] = corePlugin("memory", []string{svcMemory}, []string{svcDB}, nil, func(c *plugin.Context) error {
					return c.Provide(svcMemory, memory.Engine(stub))
				})
			}
		}
		return ps
	})
	for _, name := range []string{"agent", "toolset"} {
		if st := statusMap(rt)[name]; st.State != plugin.StateRunning {
			t.Fatalf("%s did not start against the replacement memory: %+v", name, st)
		}
	}
	tl, ok := rt.toolReg.Get("remember")
	if !ok {
		t.Fatal("remember tool is missing")
	}
	inv, ok := tl.(einotool.InvokableTool)
	if !ok {
		t.Fatalf("remember is %T, not invokable", tl)
	}
	if _, err := inv.InvokableRun(context.Background(), `{"content":"the build uses Go 1.25"}`); err != nil {
		t.Fatal(err)
	}
	if len(stub.ingested) != 1 || stub.ingested[0].Content != "the build uses Go 1.25" {
		t.Fatalf("the tool did not use the replacement memory: %+v", stub.ingested)
	}
	// Plugins that need the original memory's extras degrade instead of failing.
	if st := statusMap(rt)["distill"]; st.State != plugin.StateRunning || rt.distillScheduler != nil {
		t.Errorf("distill should run idle without a distillable memory: state=%s", st.State)
	}
}

// stubModel answers every question about which model to use with one fixed config.
type stubModel struct{ cfg LLMConfig }

func (m stubModel) LLM() LLMConfig { return m.cfg }

// The same for the model: swap the plugin and nobody who asks for a model
// changes. Callers that read the configuration directly are covered too,
// because Runtime.LLMConfig asks the host.
func TestReplacingTheModelNeedsNoChangeInItsConsumers(t *testing.T) {
	want := LLMConfig{APIBase: "http://stub.invalid/v1", APIKey: "stub-key", Model: "stub-model", FallbackModel: "stub-fallback"}
	rt := bootIn(t, t.TempDir(), func(ps []plugin.Plugin) []plugin.Plugin {
		for i, p := range ps {
			if p.Name == "model" {
				ps[i] = corePlugin("model", []string{svcModel}, nil, nil, func(c *plugin.Context) error {
					return c.Provide(svcModel, ModelSource(stubModel{cfg: want}))
				})
			}
		}
		return ps
	})
	for _, name := range []string{"model", "memory", "agent", "toolset"} {
		if st := statusMap(rt)[name]; st.State != plugin.StateRunning {
			t.Fatalf("%s: %+v", name, st)
		}
	}
	if got := rt.agentRunner.LLMSettings(); got.Model != "stub-model" || got.APIKey != "stub-key" || got.FallbackModel != "stub-fallback" {
		t.Errorf("the agent did not use the replacement model: %+v", got)
	}
	if got := rt.AgentLLMSettings(); got.Model != "stub-model" {
		t.Errorf("a direct reader of the configuration did not see the replacement: %+v", got)
	}
	if got := rt.LLMConfig(); got != want {
		t.Errorf("LLMConfig = %+v", got)
	}
	// The graph says who needs the model.
	need := map[string]bool{}
	for _, e := range rt.host.Graph().Edges {
		if e.Service == svcModel && e.Resolved {
			need[e.From] = true
		}
	}
	for _, name := range []string{"memory", "agent", "toolset"} {
		if !need[name] {
			t.Errorf("%s does not declare its dependency on the model: %v", name, need)
		}
	}
}

// With the default model plugin, changing the configuration applies without a restart.
func TestDefaultModelFollowsTheSettings(t *testing.T) {
	rt := boot(t)
	if err := rt.SetLLMConfig(LLMConfig{APIBase: "http://a.invalid", APIKey: "k", Model: "m1"}); err != nil {
		t.Fatal(err)
	}
	if got := rt.agentRunner.LLMSettings().Model; got != "m1" {
		t.Fatalf("agent sees model %q after the settings changed", got)
	}
	if err := rt.SetLLMConfig(LLMConfig{APIBase: "http://a.invalid", APIKey: "k", Model: "m2"}); err != nil {
		t.Fatal(err)
	}
	if got := rt.agentRunner.LLMSettings().Model; got != "m2" {
		t.Fatalf("agent sees model %q, want m2", got)
	}
}

func TestPluginNamesAreUniqueAndEveryServiceHasOneProvider(t *testing.T) {
	rt := &Runtime{}
	seenName := map[string]bool{}
	seenSvc := map[string]string{}
	var names []string
	for _, p := range rt.runtimePlugins() {
		if seenName[p.Name] {
			t.Errorf("plugin %q declared twice", p.Name)
		}
		seenName[p.Name] = true
		names = append(names, p.Name)
		for _, s := range p.Provides {
			if other, dup := seenSvc[s]; dup {
				t.Errorf("service %q provided by both %s and %s", s, other, p.Name)
			}
			seenSvc[s] = p.Name
		}
	}
	// Everything a plugin needs is provided by some plugin, or by core.
	coreProvided := map[string]bool{svcEngines: true}
	for _, p := range rt.runtimePlugins() {
		for _, need := range append(append([]string{}, p.Inject...), p.Optional...) {
			if _, ok := seenSvc[need]; !ok && !coreProvided[need] {
				t.Errorf("plugin %s needs %q, which nothing provides", p.Name, need)
			}
		}
	}
	sort.Strings(names)
	if len(names) != len(coreRuntimePlugins)+len(optionalRuntimePlugins) {
		t.Errorf("plugins = %v", names)
	}
}
