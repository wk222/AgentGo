package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"agentgo/internal/governance"
	"agentgo/internal/processutil"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// FileParserAdapter binds one or more file extensions (e.g. ".sqlite", ".pcap", ".bin")
// to a command-line converter that outputs structured text/markdown/JSON to stdout.
// Once registered in <workspace>/.agentgo/parsers.json, built-in `parse_file` automatically
// delegates matching files to this adapter.
type FileParserAdapter struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Extensions  []string `json:"extensions"` // e.g. [".sqlite", ".db"]
	Command     string   `json:"command"`    // e.g. "python"
	Args        []string `json:"args"`       // e.g. [".agentgo/parsers/sqlite_dump.py", "{{file}}"]
	TimeoutSec  int      `json:"timeout_sec,omitempty"`
}

// CLIAdapterSpec wraps an external CLI command with `{{param}}` placeholders in ArgsTemplate
// into a first-class Eino tool registered directly in tools.Registry.
type CLIAdapterSpec struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Command      string            `json:"command"`
	ArgsTemplate []string          `json:"args_template"` // e.g. ["-json", "{{db_path}}", "{{sql}}"]
	Env          map[string]string `json:"env,omitempty"`
	TimeoutSec   int               `json:"timeout_sec,omitempty"`
}

// LSPAdapterSpec mirrors internal/codelsp.ServerSpec for <workspace>/lsp.json.
type LSPAdapterSpec struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	FileTypes   []string          `json:"filetypes,omitempty"`
	RootMarkers []string          `json:"root_markers,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
	TimeoutSec  int               `json:"timeout,omitempty"`
}

// LLMProfileAdapter stores task-specific model routing profiles in <workspace>/.agentgo/llm_profiles.json.
type LLMProfileAdapter struct {
	Name      string `json:"name"`
	Role      string `json:"role"`     // e.g. "vision", "fast_extract", "local_ollama", "deep_reasoner"
	APIBase   string `json:"api_base"` // e.g. "http://127.0.0.1:11434/v1"
	APIKeyEnv string `json:"api_key_env,omitempty"`
	Model     string `json:"model"`
}

type manageAdapterInput struct {
	Domain      string            `json:"domain" jsonschema:"required,description=Adaptation domain: 'file_parser' (extend parse_file for custom formats) | 'lsp' (adapt language servers in lsp.json) | 'cli_tool' (wrap any CLI into a live tool) | 'llm_profile' (adapt model endpoints by role)"`
	Action      string            `json:"action" jsonschema:"required,description=Action: 'list' | 'upsert' | 'remove' | 'test' (for file_parser) | 'probe' (for lsp or llm_profile)"`
	Name        string            `json:"name,omitempty" jsonschema:"description=Adapter or tool identifier"`
	Description string            `json:"description,omitempty" jsonschema:"description=Human/LLM readable description"`
	Command     string            `json:"command,omitempty" jsonschema:"description=Executable command (for file_parser, lsp, cli_tool)"`
	Args        []string          `json:"args,omitempty" jsonschema:"description=Command arguments or template (use {{file}} for file_parser, or {{param_name}} for cli_tool)"`
	Extensions  []string          `json:"extensions,omitempty" jsonschema:"description=File extensions for file_parser (e.g. ['.sqlite','.db']) or filetypes for lsp (e.g. ['.hx','.lua'])"`
	RootMarkers []string          `json:"root_markers,omitempty" jsonschema:"description=Workspace root marker globs for lsp (e.g. ['build.hxml','*.rockspec'])"`
	Env         map[string]string `json:"env,omitempty" jsonschema:"description=Optional environment variables"`
	TestFile    string            `json:"test_file,omitempty" jsonschema:"description=Workspace-relative or absolute file path for action='test' on file_parser"`
	Role        string            `json:"role,omitempty" jsonschema:"description=Task role for llm_profile (e.g. 'vision', 'local_coder', 'fast_extract')"`
	APIBase     string            `json:"api_base,omitempty" jsonschema:"description=OpenAI-compatible base URL for llm_profile"`
	APIKeyEnv   string            `json:"api_key_env,omitempty" jsonschema:"description=Environment variable holding API key for llm_profile"`
	Model       string            `json:"model,omitempty" jsonschema:"description=Model ID for llm_profile"`
	TimeoutSec  int               `json:"timeout_sec,omitempty" jsonschema:"description=Execution timeout in seconds (default 30)"`
}

// RegisterAdapterManagerTool registers `manage_adapter` so AgentGo can self-adapt file parsers,
// LSP servers, CLI tools, and LLM provider profiles at runtime.
func RegisterAdapterManagerTool(r *Registry, workspaceRoot string) error {
	ws := strings.TrimSpace(workspaceRoot)
	if ws == "" {
		ws = "."
	}
	Declare(Spec{
		Name:     "manage_adapter",
		Source:   "builtin",
		Static:   false,
		Mutating: true,
		Risk:     governance.RiskHigh,
	})

	t, err := utils.InferTool(
		"manage_adapter",
		"Self-adaptation hub beyond MCP & channels: dynamically adapt (1) 'file_parser' so parse_file can decode custom binary/document formats (.sqlite, .pcap, .parquet, .bin, etc.), (2) 'lsp' in lsp.json for new programming languages, (3) 'cli_tool' to wrap any local CLI with {{param}} placeholders into a live registered tool, and (4) 'llm_profile' to register/probe task-specific model endpoints.",
		func(ctx context.Context, in manageAdapterInput) (string, error) {
			domain := strings.ToLower(strings.TrimSpace(in.Domain))
			action := strings.ToLower(strings.TrimSpace(in.Action))
			switch domain {
			case "file_parser":
				return handleFileParserAdapter(ctx, ws, action, in)
			case "lsp":
				return handleLSPAdapter(ctx, ws, action, in)
			case "cli_tool":
				return handleCLIToolAdapter(ctx, r, ws, action, in)
			case "llm_profile":
				return handleLLMProfileAdapter(ctx, ws, action, in)
			default:
				return "", fmt.Errorf("unknown domain %q (expected: file_parser | lsp | cli_tool | llm_profile)", in.Domain)
			}
		},
	)
	if err != nil {
		return err
	}
	r.AddTool(t)
	AutoLoadCLIAdapters(r, ws)
	return nil
}

// --- 1. File Parser Adapters (.agentgo/parsers.json) ---

func parsersConfigPath(ws string) string {
	return filepath.Join(ws, ".agentgo", "parsers.json")
}

func loadFileParsers(ws string) (map[string]FileParserAdapter, error) {
	p := parsersConfigPath(ws)
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return map[string]FileParserAdapter{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]FileParserAdapter{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func saveFileParsers(ws string, m map[string]FileParserAdapter) error {
	p := parsersConfigPath(ws)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

func normalizeExt(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	if e != "" && !strings.HasPrefix(e, ".") {
		e = "." + e
	}
	return e
}

func tryCustomFileParser(ctx context.Context, ws, absFile, ext string) (parseFileOutput, bool, error) {
	ext = normalizeExt(ext)
	if ext == "" {
		return parseFileOutput{}, false, nil
	}
	parsers, err := loadFileParsers(ws)
	if err != nil || len(parsers) == 0 {
		return parseFileOutput{}, false, nil
	}
	var matched *FileParserAdapter
	for _, ad := range parsers {
		for _, candidate := range ad.Extensions {
			if normalizeExt(candidate) == ext {
				copyAd := ad
				matched = &copyAd
				break
			}
		}
		if matched != nil {
			break
		}
	}
	if matched == nil {
		return parseFileOutput{}, false, nil
	}
	outText, err := runFileParserCommand(ctx, ws, *matched, absFile)
	if err != nil {
		return parseFileOutput{}, true, fmt.Errorf("custom file_parser %q failed on %s: %w", matched.Name, filepath.Base(absFile), err)
	}
	return parseFileOutput{
		Text:      outText,
		FileType:  matched.Name,
		CharCount: charCount(outText),
	}, true, nil
}

func runFileParserCommand(ctx context.Context, ws string, ad FileParserAdapter, absFile string) (string, error) {
	timeout := ad.TimeoutSec
	if timeout <= 0 {
		timeout = 30
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	var args []string
	replaced := false
	for _, a := range ad.Args {
		if strings.Contains(a, "{{file}}") {
			args = append(args, strings.ReplaceAll(a, "{{file}}", absFile))
			replaced = true
		} else {
			args = append(args, a)
		}
	}
	if !replaced {
		args = append(args, absFile)
	}

	cmd := exec.CommandContext(runCtx, ad.Command, args...)
	processutil.HideWindow(cmd)
	cmd.Dir = ws
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func handleFileParserAdapter(ctx context.Context, ws, action string, in manageAdapterInput) (string, error) {
	parsers, err := loadFileParsers(ws)
	if err != nil {
		return "", err
	}
	switch action {
	case "list":
		b, _ := json.MarshalIndent(map[string]any{
			"domain":      "file_parser",
			"config_path": parsersConfigPath(ws),
			"adapters":    parsers,
		}, "", "  ")
		return string(b), nil

	case "upsert":
		name := strings.TrimSpace(in.Name)
		if name == "" || strings.TrimSpace(in.Command) == "" || len(in.Extensions) == 0 {
			return "", fmt.Errorf("file_parser upsert requires name, command, and extensions")
		}
		exts := make([]string, 0, len(in.Extensions))
		for _, e := range in.Extensions {
			if ne := normalizeExt(e); ne != "" {
				exts = append(exts, ne)
			}
		}
		ad := FileParserAdapter{
			Name:        name,
			Description: strings.TrimSpace(in.Description),
			Extensions:  exts,
			Command:     strings.TrimSpace(in.Command),
			Args:        in.Args,
			TimeoutSec:  in.TimeoutSec,
		}
		parsers[name] = ad
		if err := saveFileParsers(ws, parsers); err != nil {
			return "", err
		}
		b, _ := json.MarshalIndent(map[string]any{
			"status":  "registered",
			"domain":  "file_parser",
			"adapter": ad,
			"hint":    "Built-in `parse_file` will now automatically use this adapter for " + strings.Join(exts, ", "),
		}, "", "  ")
		return string(b), nil

	case "remove":
		name := strings.TrimSpace(in.Name)
		delete(parsers, name)
		if err := saveFileParsers(ws, parsers); err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"status":"removed","domain":"file_parser","name":%q}`, name), nil

	case "test":
		name := strings.TrimSpace(in.Name)
		ad, ok := parsers[name]
		if !ok {
			return "", fmt.Errorf("file_parser %q not found", name)
		}
		target := strings.TrimSpace(in.TestFile)
		if target == "" {
			return "", fmt.Errorf("test_file is required for action='test'")
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(ws, target)
		}
		out, err := runFileParserCommand(ctx, ws, ad, target)
		if err != nil {
			return "", err
		}
		b, _ := json.MarshalIndent(map[string]any{
			"status":     "ok",
			"adapter":    name,
			"test_file":  target,
			"char_count": charCount(out),
			"preview":    out,
		}, "", "  ")
		return string(b), nil

	default:
		return "", fmt.Errorf("unsupported file_parser action %q (expected: list | upsert | remove | test)", action)
	}
}

// --- 2. LSP Language Server Adapters (<workspace>/lsp.json) ---

func lspConfigPath(ws string) string {
	return filepath.Join(ws, "lsp.json")
}

func loadLSPAdapters(ws string) (map[string]LSPAdapterSpec, error) {
	p := lspConfigPath(ws)
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return map[string]LSPAdapterSpec{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]LSPAdapterSpec{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func saveLSPAdapters(ws string, m map[string]LSPAdapterSpec) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(lspConfigPath(ws), b, 0o644)
}

func handleLSPAdapter(_ context.Context, ws, action string, in manageAdapterInput) (string, error) {
	specs, err := loadLSPAdapters(ws)
	if err != nil {
		return "", err
	}
	switch action {
	case "list":
		b, _ := json.MarshalIndent(map[string]any{
			"domain":      "lsp",
			"config_path": lspConfigPath(ws),
			"servers":     specs,
		}, "", "  ")
		return string(b), nil

	case "probe":
		candidates := []struct {
			Name      string   `json:"name"`
			Command   string   `json:"command"`
			Args      []string `json:"args,omitempty"`
			FileTypes []string `json:"filetypes"`
		}{
			{"gopls", "gopls", nil, []string{".go"}},
			{"pyright", "pyright-langserver", []string{"--stdio"}, []string{".py"}},
			{"pylsp", "pylsp", nil, []string{".py"}},
			{"tsserver", "typescript-language-server", []string{"--stdio"}, []string{".ts", ".tsx", ".js", ".jsx"}},
			{"clangd", "clangd", nil, []string{".c", ".cpp", ".h", ".hpp"}},
			{"rust-analyzer", "rust-analyzer", nil, []string{".rs"}},
			{"lua_ls", "lua-language-server", nil, []string{".lua"}},
			{"omnisharp", "OmniSharp", []string{"-lsp"}, []string{".cs"}},
			{"zls", "zls", nil, []string{".zig"}},
		}
		type probeHit struct {
			Name      string   `json:"name"`
			Command   string   `json:"command"`
			Path      string   `json:"path,omitempty"`
			Found     bool     `json:"found"`
			FileTypes []string `json:"filetypes"`
		}
		var hits []probeHit
		for _, c := range candidates {
			resolved, err := exec.LookPath(c.Command)
			hits = append(hits, probeHit{
				Name:      c.Name,
				Command:   c.Command,
				Path:      resolved,
				Found:     err == nil,
				FileTypes: c.FileTypes,
			})
		}
		b, _ := json.MarshalIndent(map[string]any{
			"domain":     "lsp",
			"discovered": hits,
		}, "", "  ")
		return string(b), nil

	case "upsert":
		name := strings.TrimSpace(in.Name)
		if name == "" || strings.TrimSpace(in.Command) == "" {
			return "", fmt.Errorf("lsp upsert requires name and command")
		}
		sp := LSPAdapterSpec{
			Command:     strings.TrimSpace(in.Command),
			Args:        in.Args,
			Env:         in.Env,
			FileTypes:   in.Extensions,
			RootMarkers: in.RootMarkers,
			TimeoutSec:  in.TimeoutSec,
		}
		specs[name] = sp
		if err := saveLSPAdapters(ws, specs); err != nil {
			return "", err
		}
		b, _ := json.MarshalIndent(map[string]any{
			"status":      "configured",
			"domain":      "lsp",
			"config_path": lspConfigPath(ws),
			"server":      sp,
		}, "", "  ")
		return string(b), nil

	case "remove":
		name := strings.TrimSpace(in.Name)
		delete(specs, name)
		if err := saveLSPAdapters(ws, specs); err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"status":"removed","domain":"lsp","name":%q}`, name), nil

	default:
		return "", fmt.Errorf("unsupported lsp action %q (expected: list | probe | upsert | remove)", action)
	}
}

// --- 3. CLI Tool Wrappers (.agentgo/cli_adapters.json) ---

var cliPlaceholderRe = regexp.MustCompile(`\{\{([a-zA-Z0-9_]+)\}\}`)

func cliAdaptersConfigPath(ws string) string {
	return filepath.Join(ws, ".agentgo", "cli_adapters.json")
}

func loadCLIAdapters(ws string) (map[string]CLIAdapterSpec, error) {
	p := cliAdaptersConfigPath(ws)
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return map[string]CLIAdapterSpec{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]CLIAdapterSpec{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func saveCLIAdapters(ws string, m map[string]CLIAdapterSpec) error {
	p := cliAdaptersConfigPath(ws)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

// AutoLoadCLIAdapters loads persisted CLI tool wrappers from <workspace>/.agentgo/cli_adapters.json
// into the live Registry.
func AutoLoadCLIAdapters(r *Registry, ws string) {
	if r == nil {
		return
	}
	adapters, err := loadCLIAdapters(ws)
	if err != nil {
		return
	}
	for _, spec := range adapters {
		if ValidToolName(spec.Name) {
			r.AddTool(newCLIWrapperTool(ws, spec))
		}
	}
}

type cliWrapperTool struct {
	ws   string
	spec CLIAdapterSpec
}

func newCLIWrapperTool(ws string, spec CLIAdapterSpec) tool.InvokableTool {
	return &cliWrapperTool{ws: ws, spec: spec}
}

func (c *cliWrapperTool) extractParams() []string {
	seen := map[string]bool{}
	var params []string
	for _, arg := range c.spec.ArgsTemplate {
		matches := cliPlaceholderRe.FindAllStringSubmatch(arg, -1)
		for _, m := range matches {
			if len(m) > 1 && !seen[m[1]] {
				seen[m[1]] = true
				params = append(params, m[1])
			}
		}
	}
	sort.Strings(params)
	return params
}

func (c *cliWrapperTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	params := map[string]*schema.ParameterInfo{}
	for _, p := range c.extractParams() {
		params[p] = &schema.ParameterInfo{
			Type:     schema.String,
			Desc:     "Template parameter {{" + p + "}} for CLI adapter " + c.spec.Name,
			Required: true,
		}
	}
	desc := strings.TrimSpace(c.spec.Description)
	if desc == "" {
		desc = fmt.Sprintf("Self-adapted CLI wrapper around `%s`", c.spec.Command)
	}
	return &schema.ToolInfo{
		Name:        c.spec.Name,
		Desc:        desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

func (c *cliWrapperTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var vals map[string]any
	if strings.TrimSpace(argumentsInJSON) != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &vals); err != nil {
			return "", fmt.Errorf("invalid JSON arguments: %w", err)
		}
	}
	timeout := c.spec.TimeoutSec
	if timeout <= 0 {
		timeout = 30
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	args := make([]string, 0, len(c.spec.ArgsTemplate))
	for _, tpl := range c.spec.ArgsTemplate {
		rendered := cliPlaceholderRe.ReplaceAllStringFunc(tpl, func(match string) string {
			sub := cliPlaceholderRe.FindStringSubmatch(match)
			if len(sub) > 1 {
				if v, ok := vals[sub[1]]; ok && v != nil {
					return fmt.Sprint(v)
				}
			}
			return ""
		})
		args = append(args, rendered)
	}

	cmd := exec.CommandContext(runCtx, c.spec.Command, args...)
	processutil.HideWindow(cmd)
	cmd.Dir = c.ws
	if len(c.spec.Env) > 0 {
		env := os.Environ()
		for k, v := range c.spec.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	resp := map[string]any{
		"tool":   c.spec.Name,
		"stdout": strings.TrimSpace(stdout.String()),
		"stderr": strings.TrimSpace(stderr.String()),
	}
	if err != nil {
		resp["error"] = err.Error()
	}
	b, _ := json.MarshalIndent(resp, "", "  ")
	return string(b), nil
}

func handleCLIToolAdapter(_ context.Context, r *Registry, ws, action string, in manageAdapterInput) (string, error) {
	adapters, err := loadCLIAdapters(ws)
	if err != nil {
		return "", err
	}
	switch action {
	case "list":
		b, _ := json.MarshalIndent(map[string]any{
			"domain":      "cli_tool",
			"config_path": cliAdaptersConfigPath(ws),
			"adapters":    adapters,
		}, "", "  ")
		return string(b), nil

	case "upsert":
		name := strings.TrimSpace(in.Name)
		if !ValidToolName(name) {
			return "", fmt.Errorf("invalid tool name %q", name)
		}
		if strings.TrimSpace(in.Command) == "" {
			return "", fmt.Errorf("cli_tool upsert requires command")
		}
		spec := CLIAdapterSpec{
			Name:         name,
			Description:  strings.TrimSpace(in.Description),
			Command:      strings.TrimSpace(in.Command),
			ArgsTemplate: in.Args,
			Env:          in.Env,
			TimeoutSec:   in.TimeoutSec,
		}
		adapters[name] = spec
		if err := saveCLIAdapters(ws, adapters); err != nil {
			return "", err
		}
		wrapper := newCLIWrapperTool(ws, spec)
		if r != nil {
			r.AddTool(wrapper)
		}
		b, _ := json.MarshalIndent(map[string]any{
			"status":     "registered_and_persisted",
			"domain":     "cli_tool",
			"tool_name":  name,
			"parameters": wrapper.(*cliWrapperTool).extractParams(),
		}, "", "  ")
		return string(b), nil

	case "remove":
		name := strings.TrimSpace(in.Name)
		delete(adapters, name)
		if err := saveCLIAdapters(ws, adapters); err != nil {
			return "", err
		}
		if r != nil {
			r.RemoveTool(name)
		}
		return fmt.Sprintf(`{"status":"removed","domain":"cli_tool","name":%q}`, name), nil

	default:
		return "", fmt.Errorf("unsupported cli_tool action %q (expected: list | upsert | remove)", action)
	}
}

// --- 4. LLM Profile Adapters (.agentgo/llm_profiles.json) ---

func llmProfilesConfigPath(ws string) string {
	return filepath.Join(ws, ".agentgo", "llm_profiles.json")
}

func loadLLMProfiles(ws string) (map[string]LLMProfileAdapter, error) {
	p := llmProfilesConfigPath(ws)
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return map[string]LLMProfileAdapter{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]LLMProfileAdapter{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func saveLLMProfiles(ws string, m map[string]LLMProfileAdapter) error {
	p := llmProfilesConfigPath(ws)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

func handleLLMProfileAdapter(ctx context.Context, ws, action string, in manageAdapterInput) (string, error) {
	profiles, err := loadLLMProfiles(ws)
	if err != nil {
		return "", err
	}
	switch action {
	case "list":
		b, _ := json.MarshalIndent(map[string]any{
			"domain":      "llm_profile",
			"config_path": llmProfilesConfigPath(ws),
			"profiles":    profiles,
		}, "", "  ")
		return string(b), nil

	case "upsert":
		name := strings.TrimSpace(in.Name)
		if name == "" || strings.TrimSpace(in.APIBase) == "" {
			return "", fmt.Errorf("llm_profile upsert requires name and api_base")
		}
		prof := LLMProfileAdapter{
			Name:      name,
			Role:      strings.TrimSpace(in.Role),
			APIBase:   strings.TrimRight(strings.TrimSpace(in.APIBase), "/"),
			APIKeyEnv: strings.TrimSpace(in.APIKeyEnv),
			Model:     strings.TrimSpace(in.Model),
		}
		profiles[name] = prof
		if err := saveLLMProfiles(ws, profiles); err != nil {
			return "", err
		}
		b, _ := json.MarshalIndent(map[string]any{
			"status":  "saved",
			"domain":  "llm_profile",
			"profile": prof,
		}, "", "  ")
		return string(b), nil

	case "remove":
		name := strings.TrimSpace(in.Name)
		delete(profiles, name)
		if err := saveLLMProfiles(ws, profiles); err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"status":"removed","domain":"llm_profile","name":%q}`, name), nil

	case "probe":
		base := strings.TrimRight(strings.TrimSpace(in.APIBase), "/")
		keyEnv := strings.TrimSpace(in.APIKeyEnv)
		if base == "" && in.Name != "" {
			if p, ok := profiles[strings.TrimSpace(in.Name)]; ok {
				base = p.APIBase
				keyEnv = p.APIKeyEnv
			}
		}
		if base == "" {
			base = "http://127.0.0.1:11434/v1" // default probe local Ollama/vLLM OpenAI endpoint
		}
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, base+"/models", nil)
		if err != nil {
			return "", err
		}
		if keyEnv != "" {
			if k := os.Getenv(keyEnv); k != "" {
				req.Header.Set("Authorization", "Bearer "+k)
			}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			b, _ := json.MarshalIndent(map[string]any{
				"reachable": false,
				"api_base":  base,
				"error":     err.Error(),
			}, "", "  ")
			return string(b), nil
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		var parsed struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		var models []string
		if json.Unmarshal(body, &parsed) == nil {
			for _, m := range parsed.Data {
				if m.ID != "" {
					models = append(models, m.ID)
				}
			}
		}
		b, _ := json.MarshalIndent(map[string]any{
			"reachable":   resp.StatusCode < 400,
			"http_status": resp.StatusCode,
			"api_base":    base,
			"models":      models,
		}, "", "  ")
		return string(b), nil

	default:
		return "", fmt.Errorf("unsupported llm_profile action %q (expected: list | upsert | remove | probe)", action)
	}
}
