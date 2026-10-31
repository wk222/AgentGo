package tools

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

func newDynStoreAndRegistry(t *testing.T) (*DynamicStore, *Registry) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewDynamicStore(db)
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.SetDynamicSandbox(t.TempDir())
	return store, reg
}

func TestValidatePythonSyntaxRejectsInvalidCode(t *testing.T) {
	err := ValidatePythonSyntax(context.Background(), "def broken(:\n    pass\n")
	if err == nil || !strings.Contains(err.Error(), "python syntax invalid") {
		t.Fatalf("expected syntax error, got %v", err)
	}
	if err := ValidatePythonSyntax(context.Background(), "print(args)\n"); err != nil {
		t.Fatalf("valid code rejected: %v", err)
	}
}

func TestCreateToolInvalidPythonIsNotPersisted(t *testing.T) {
	store, reg := newDynStoreAndRegistry(t)
	onCompiled := func(def DynamicToolDef) error { return reg.RegisterDynamicTool(def) }
	if err := registerCreateTool(reg, store, nil, onCompiled); err != nil {
		t.Fatal(err)
	}
	out, err := reg.InvokeJSON(context.Background(), "create_tool",
		`{"tool_name":"bad_tool","description":"broken","parameters":"{}","code":"def broken(:\n"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"success":false`) {
		t.Fatalf("invalid python must not succeed: %s", out)
	}
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("invalid tool was persisted: %+v", list)
	}
	if _, ok := reg.Get("bad_tool"); ok {
		t.Fatal("invalid tool was registered")
	}
}

func TestCreateToolRegistrationFailureRollsBack(t *testing.T) {
	store, reg := newDynStoreAndRegistry(t)
	fail := false
	onCompiled := func(def DynamicToolDef) error {
		if fail {
			return fmt.Errorf("boom")
		}
		return reg.RegisterDynamicTool(def)
	}
	if err := registerCreateTool(reg, store, nil, onCompiled); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	good := `{"tool_name":"echo_tool","description":"echo v1","parameters":"{}","code":"print(args)"}`
	if out, err := reg.InvokeJSON(ctx, "create_tool", good); err != nil || !strings.Contains(out, `"success":true`) {
		t.Fatalf("v1 create failed: %s err=%v", out, err)
	}

	// Updating with a failing registration restores the previous definition.
	fail = true
	v2 := `{"tool_name":"echo_tool","description":"echo v2","parameters":"{}","code":"print('v2')"}`
	out, err := reg.InvokeJSON(ctx, "create_tool", v2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"success":false`) {
		t.Fatalf("failing registration must report failure: %s", out)
	}
	def, err := store.Get("echo_tool")
	if err != nil {
		t.Fatal(err)
	}
	if def.Description != "echo v1" {
		t.Fatalf("rollback lost previous version: %+v", def)
	}

	// A brand-new tool whose registration fails leaves nothing behind.
	newTool := `{"tool_name":"ghost_tool","description":"ghost","parameters":"{}","code":"print(1)"}`
	if out, err := reg.InvokeJSON(ctx, "create_tool", newTool); err != nil || !strings.Contains(out, `"success":false`) {
		t.Fatalf("ghost create should fail: %s err=%v", out, err)
	}
	if _, err := store.Get("ghost_tool"); err == nil {
		t.Fatal("ghost_tool persisted despite failed registration")
	}
}

// Concurrent invocations of the same tool must each see their own arguments:
// args/wrapper files are per-invocation, not shared <tool>_args.json.
func TestDynamicToolConcurrentInvocationsDoNotShareArgs(t *testing.T) {
	store, reg := newDynStoreAndRegistry(t)
	onCompiled := func(def DynamicToolDef) error { return reg.RegisterDynamicTool(def) }
	if err := registerCreateTool(reg, store, nil, onCompiled); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	create := `{"tool_name":"echo_args","description":"echo","parameters":"{}","code":"import json\nprint(json.dumps({'echo': args['x']}))"}`
	if out, err := reg.InvokeJSON(ctx, "create_tool", create); err != nil || !strings.Contains(out, `"success":true`) {
		t.Fatalf("create failed: %s err=%v", out, err)
	}

	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := reg.InvokeJSON(ctx, "echo_args", fmt.Sprintf(`{"arguments":"{\"x\": %d}"}`, i))
			if err != nil {
				errs[i] = err
				return
			}
			if !strings.Contains(out, fmt.Sprintf(`\"echo\": %d`, i)) && !strings.Contains(out, fmt.Sprintf(`"echo": %d`, i)) {
				errs[i] = fmt.Errorf("call %d saw wrong args: %s", i, out)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
}

// Tool processes must not inherit host secrets by default; a variable the
// tool declared at creation time is passed through, and only that one.
func TestBuildToolEnvFiltersSecretsUnlessDeclared(t *testing.T) {
	t.Setenv("AGENTGO_GATEWAY_TOKEN", "should-not-leak")
	t.Setenv("SOME_API_KEY", "should-not-leak")
	t.Setenv("WEATHER_API_KEY", "declared-value")

	env := buildToolEnv(DynamicToolDef{Name: "t"}, "AGENTGO_ARGS_FILE=/tmp/args.json")
	joined := strings.Join(env, "\n")
	for _, leaked := range []string{"AGENTGO_GATEWAY_TOKEN", "SOME_API_KEY", "WEATHER_API_KEY"} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("undeclared variable %s leaked into tool env", leaked)
		}
	}
	if !strings.Contains(joined, "AGENTGO_ARGS_FILE=/tmp/args.json") {
		t.Fatal("args file extra missing")
	}
	if !strings.Contains(joined, "PATH=") {
		t.Fatal("base allowlist (PATH) missing")
	}

	env = buildToolEnv(DynamicToolDef{Name: "t", EnvVars: []string{"WEATHER_API_KEY"}})
	joined = strings.Join(env, "\n")
	if !strings.Contains(joined, "WEATHER_API_KEY=declared-value") {
		t.Fatal("declared variable not passed through")
	}
	if strings.Contains(joined, "AGENTGO_GATEWAY_TOKEN") {
		t.Fatal("undeclared secret still leaked with declarations present")
	}
}

// The env declaration round-trips through the store and create_tool.
func TestEnvVarsPersistThroughCreateAndStore(t *testing.T) {
	store, reg := newDynStoreAndRegistry(t)
	onCompiled := func(def DynamicToolDef) error { return reg.RegisterDynamicTool(def) }
	if err := registerCreateTool(reg, store, nil, onCompiled); err != nil {
		t.Fatal(err)
	}
	out, err := reg.InvokeJSON(context.Background(), "create_tool",
		`{"tool_name":"needs_key","description":"d","parameters":"{}","code":"print(1)","env_vars":"WEATHER_API_KEY, OTHER_TOKEN"}`)
	if err != nil || !strings.Contains(out, `"success":true`) {
		t.Fatalf("create failed: %s err=%v", out, err)
	}
	def, err := store.Get("needs_key")
	if err != nil {
		t.Fatal(err)
	}
	if len(def.EnvVars) != 2 || def.EnvVars[0] != "WEATHER_API_KEY" || def.EnvVars[1] != "OTHER_TOKEN" {
		t.Fatalf("env vars not persisted: %+v", def.EnvVars)
	}
	if _, err := ParseEnvVars("OK_NAME, 9BAD"); err == nil {
		t.Fatal("invalid env name must be rejected")
	}
}

func TestSyncDynamicFromStoreSkipsInvalidButKeepsValid(t *testing.T) {
	store, reg := newDynStoreAndRegistry(t)
	if err := store.Save(DynamicToolDef{Name: "valid_tool", Description: "ok", Parameters: "{}", Code: "print(1)"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(DynamicToolDef{Name: "broken_tool", Description: "bad", Parameters: "{}", Code: "def broken(:"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SyncDynamicFromStore(store); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("valid_tool"); !ok {
		t.Fatal("valid tool not restored")
	}
	if _, ok := reg.Get("broken_tool"); ok {
		t.Fatal("broken tool must not be restored")
	}
	// The broken definition stays in the store (visible, fixable) — restore
	// only refuses to register it.
	if _, err := store.Get("broken_tool"); err != nil {
		t.Fatal("broken definition should remain stored for inspection")
	}
}

// Validation must neither run Python startup hooks nor execute the tool body.
func TestValidatePythonSyntaxIsolatedStartup(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "startup-marker")
	t.Setenv("PYTHONPATH", dir)
	t.Setenv("AGENTGO_REVIEW_DUMMY_SECRET", "dummy-secret")
	t.Setenv("AGENTGO_REVIEW_MARKER", marker)
	hook := "import os\nwith open(os.environ['AGENTGO_REVIEW_MARKER'], 'w') as f: f.write(os.environ.get('AGENTGO_REVIEW_DUMMY_SECRET', 'absent'))\n"
	if err := os.WriteFile(filepath.Join(dir, "sitecustomize.py"), []byte(hook), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePythonSyntax(context.Background(), hook); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("validator executed startup hook or tool body: %v", err)
	}
}

func TestValidatePythonSyntaxRejectsCompileOnlyErrors(t *testing.T) {
	for _, code := range []string{"return 1", "break", "continue", "nonlocal missing"} {
		if err := ValidatePythonSyntax(context.Background(), code); err == nil {
			t.Fatalf("accepted non-executable code %q", code)
		}
	}
}

func TestDynamicToolOutputIsBounded(t *testing.T) {
	out, err := runPythonToolBody(context.Background(), t.TempDir(), DynamicToolDef{Name: "verbose_tool", Code: "print('x' * (3 * 1024 * 1024))"}, "{}")
	if err != nil || out.Error != "" || out.ExitCode != 0 {
		t.Fatalf("execution failed: %+v %v", out, err)
	}
	if len(out.Stdout) > 1024*1024+100 || !strings.Contains(out.Stdout, "output truncated") {
		t.Fatalf("output not bounded: %d bytes", len(out.Stdout))
	}
}
