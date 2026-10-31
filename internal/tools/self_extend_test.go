package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
)

func TestSelfExtendMCPScaffoldRegisterInvokeAndRemove(t *testing.T) {
	pyExe, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python not on PATH")
	}

	ws := t.TempDir()
	r := NewRegistry()
	if err := RegisterMCPManagerTool(r, ws); err != nil {
		t.Fatal(err)
	}
	mgr, ok := r.Get("manage_mcp_server")
	if !ok {
		t.Fatal("manage_mcp_server not registered")
	}
	inv := mgr.(einotool.InvokableTool)
	ctx := context.Background()

	// 1. Agent scaffolds a new MCP server into the workspace.
	scafOut, err := inv.InvokableRun(ctx, `{"action":"scaffold","name":"auto_ext","description":"self-generated MCP"}`)
	if err != nil {
		t.Fatal(err)
	}
	var scaf mcpManagerOutput
	if err := json.Unmarshal([]byte(scafOut), &scaf); err != nil || !scaf.Success || scaf.ScaffoldPath == "" {
		t.Fatalf("scaffold failed: %s err=%v", scafOut, err)
	}

	// 2. Agent hot-registers the newly scaffolded MCP server at runtime.
	regArgs, _ := json.Marshal(mcpManagerInput{
		Action:  "register",
		Name:    "auto_ext",
		Command: pyExe,
		Args:    []string{scaf.ScaffoldPath},
	})
	regOut, err := inv.InvokableRun(ctx, string(regArgs))
	if err != nil {
		t.Fatal(err)
	}
	var regRes mcpManagerOutput
	if err := json.Unmarshal([]byte(regOut), &regRes); err != nil || !regRes.Success {
		t.Fatalf("register failed: %s err=%v", regOut, err)
	}
	if len(regRes.Tools) != 1 || regRes.Tools[0] != "auto_ext_ping" {
		t.Fatalf("expected [auto_ext_ping], got %+v", regRes.Tools)
	}

	// 3. Agent immediately invokes the newly mounted MCP tool in the same process.
	pingTool, ok := r.Get("auto_ext_ping")
	if !ok {
		t.Fatal("auto_ext_ping not found in registry after hot-register")
	}
	pingOut, err := pingTool.(einotool.InvokableTool).InvokableRun(ctx, `{"input":"hello-self-evolve"}`)
	if err != nil || !strings.Contains(pingOut, "hello-self-evolve") {
		t.Fatalf("ping output = %s, err = %v", pingOut, err)
	}

	// 4. Agent unloads the MCP server.
	remOut, err := inv.InvokableRun(ctx, `{"action":"remove","name":"auto_ext"}`)
	if err != nil || !strings.Contains(remOut, `"success":true`) {
		t.Fatalf("remove failed: %s err=%v", remOut, err)
	}
	if _, ok := r.Get("auto_ext_ping"); ok {
		t.Fatal("auto_ext_ping survived remove")
	}
}

func TestSelfExtendChannelManagerTool(t *testing.T) {
	ws := t.TempDir()
	r := NewRegistry()
	if err := RegisterChannelManagerTool(r, ws); err != nil {
		t.Fatal(err)
	}
	chTool, ok := r.Get("manage_channel")
	if !ok {
		t.Fatal("manage_channel not registered")
	}
	inv := chTool.(einotool.InvokableTool)
	ctx := context.Background()

	upOut, err := inv.InvokableRun(ctx, `{
		"action": "upsert",
		"name": "re_alert",
		"kind": "feishu",
		"webhook_url": "https://open.feishu.cn/open-apis/bot/v2/hook/demo",
		"secret": "sec_demo"
	}`)
	if err != nil || !strings.Contains(upOut, `"success":true`) {
		t.Fatalf("upsert failed: %s err=%v", upOut, err)
	}

	sendOut, err := inv.InvokableRun(ctx, `{
		"action": "send",
		"name": "re_alert",
		"title": "Gate Passed",
		"text": "gate-0001 verified SHA-256 parity",
		"dry_run": true
	}`)
	if err != nil || !strings.Contains(sendOut, `"dry_run":true`) || !strings.Contains(sendOut, "gate-0001") {
		t.Fatalf("send dry_run failed: %s err=%v", sendOut, err)
	}
}

func TestSelfAdaptFileParserCLIAndLSP(t *testing.T) {
	pyExe, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python not on PATH")
	}

	ws := t.TempDir()
	r := NewRegistry()
	if err := registerParseFile(r, ws); err != nil {
		t.Fatal(err)
	}
	if err := RegisterAdapterManagerTool(r, ws); err != nil {
		t.Fatal(err)
	}
	if err := RegisterSkillEvolveTool(r, ws); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// 1. Write a custom binary file (.mybin) and a tiny Python parser for it.
	binPath := filepath.Join(ws, "asset.mybin")
	if err := os.WriteFile(binPath, []byte("MYBIN\x00\x01\x02SECRET_PAYLOAD_42"), 0o644); err != nil {
		t.Fatal(err)
	}
	parserScript := filepath.Join(ws, "parse_mybin.py")
	scriptCode := "import sys\ndata = open(sys.argv[1], 'rb').read()\nprint('PARSED_MYBIN:' + data[8:].decode('ascii'))\n"
	if err := os.WriteFile(parserScript, []byte(scriptCode), 0o644); err != nil {
		t.Fatal(err)
	}

	// Register the custom file_parser adapter via manage_adapter.
	upParserReq, _ := json.Marshal(manageAdapterInput{
		Domain:      "file_parser",
		Action:      "upsert",
		Name:        "mybin_decoder",
		Description: "Decodes proprietary .mybin asset files",
		Extensions:  []string{".mybin"},
		Command:     pyExe,
		Args:        []string{parserScript, "{{file}}"},
	})
	if _, err := r.InvokeJSON(ctx, "manage_adapter", string(upParserReq)); err != nil {
		t.Fatal(err)
	}

	// Verify built-in parse_file now automatically routes .mybin through the custom adapter!
	parsedJSON, err := r.InvokeJSON(ctx, "parse_file", `{"relative_path":"asset.mybin"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(parsedJSON, "PARSED_MYBIN:SECRET_PAYLOAD_42") || !strings.Contains(parsedJSON, `"file_type":"mybin_decoder"`) {
		t.Fatalf("unexpected parse_file output: %s", parsedJSON)
	}

	// 2. Wrap a CLI command into a first-class registered Eino tool via domain="cli_tool".
	upCLIReq, _ := json.Marshal(manageAdapterInput{
		Domain:      "cli_tool",
		Action:      "upsert",
		Name:        "cli_py_upper",
		Description: "Converts {{word}} to uppercase via Python CLI",
		Command:     pyExe,
		Args:        []string{"-c", "import sys; print('UPPER=' + sys.argv[1].upper())", "{{word}}"},
	})
	if _, err := r.InvokeJSON(ctx, "manage_adapter", string(upCLIReq)); err != nil {
		t.Fatal(err)
	}

	cliOut, err := r.InvokeJSON(ctx, "cli_py_upper", `{"word":"self_adapted"}`)
	if err != nil || !strings.Contains(cliOut, "UPPER=SELF_ADAPTED") {
		t.Fatalf("cli_py_upper output = %s, err = %v", cliOut, err)
	}

	// 3. Adapt LSP configuration (<workspace>/lsp.json) via domain="lsp".
	upLSPReq, _ := json.Marshal(manageAdapterInput{
		Domain:      "lsp",
		Action:      "upsert",
		Name:        "haxe",
		Command:     "vshaxe-server",
		Extensions:  []string{".hx"},
		RootMarkers: []string{"build.hxml"},
	})
	if _, err := r.InvokeJSON(ctx, "manage_adapter", string(upLSPReq)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, "lsp.json")); err != nil {
		t.Fatalf("lsp.json was not written: %v", err)
	}

	// 4. Crystallize the newly learned workflow into a workspace SKILL.md via evolve_skill(action="save").
	saveSkillOut, err := r.InvokeJSON(ctx, "evolve_skill", `{
		"action": "save",
		"skill_name": "mybin-reverse",
		"code_snippet": "# MyBin Reverse Skill\n\nUse parse_file on .mybin after registering mybin_decoder."
	}`)
	if err != nil || !strings.Contains(saveSkillOut, "skill:mybin-reverse") {
		t.Fatalf("evolve_skill save failed: %s err=%v", saveSkillOut, err)
	}
	listSkillOut, err := r.InvokeJSON(ctx, "evolve_skill", `{"action":"list"}`)
	if err != nil || !strings.Contains(listSkillOut, "mybin-reverse") {
		t.Fatalf("evolve_skill list failed: %s err=%v", listSkillOut, err)
	}
}
