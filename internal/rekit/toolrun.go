package rekit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"agentgo/internal/processutil"
)

// ToolSpec describes one external analysis program the kit may launch.
type ToolSpec struct {
	Name     string   `json:"name"`
	Class    string   `json:"class"` // static (reads files) | dynamic (touches devices/processes)
	Exes     []string `json:"exes"`
	Dirs     []string `json:"dirs,omitempty"` // extra places to look (Windows install roots)
	Version  []string `json:"version_args,omitempty"`
	Env      []string `json:"-"` // extra environment for this tool
	Purpose  string   `json:"purpose"`
	Resolved string   `json:"resolved,omitempty"`
}

// catalog is an allowlist: re_tool_run refuses anything not named here. There is no
// generic shell and no interpreter (python/dotnet/node) on purpose.
//
// Dirs holds conventional install roots only. A tool that lives anywhere else is pointed at with
// RE_TOOL_<NAME> or an entry in <home>/tools.json (see resolve), never with a folder from one
// person's machine written in here.
var catalog = []ToolSpec{
	{Name: "ghidra-headless", Class: "static", Exes: []string{"analyzeHeadless.bat", "analyzeHeadless"}, Purpose: "Ghidra headless import/analyze/export script"},
	{Name: "jadx", Class: "static", Exes: []string{"jadx.bat", "jadx"}, Version: []string{"--version"}, Purpose: "APK/DEX -> Java"},
	{Name: "apktool", Class: "static", Exes: []string{"apktool.bat", "apktool"}, Version: []string{"--version"}, Purpose: "APK resources/smali"},
	{Name: "ilspycmd", Class: "static", Exes: []string{"ilspycmd.exe", "ilspycmd"}, Version: []string{"--version"}, Env: []string{"DOTNET_ROLL_FORWARD=Major"}, Purpose: ".NET assembly -> C#"},
	{Name: "rizin", Class: "static", Exes: []string{"rizin.exe", "rizin"}, Version: []string{"-v"}, Purpose: "native disasm/analysis (batch with -qc)"},
	{Name: "r2", Class: "static", Exes: []string{"radare2.exe", "r2.exe", "r2"}, Version: []string{"-v"}, Purpose: "radare2 (batch with -qc)"},
	{Name: "idat", Class: "static", Exes: []string{"idat.exe", "idat64.exe", "idat"}, Purpose: "IDA text-mode (-A -S script)"},
	{Name: "objdump", Class: "static", Exes: []string{"objdump.exe", "objdump"}, Version: []string{"--version"}, Purpose: "GNU objdump"},
	{Name: "diec", Class: "static", Exes: []string{"diec.exe", "diec"}, Purpose: "Detect-It-Easy console (packer/compiler id)"},
	{Name: "capa", Class: "static", Exes: []string{"capa.exe", "capa"}, Version: []string{"--version"}, Purpose: "capability detection"},
	{Name: "7z", Class: "static", Exes: []string{"7z.exe", "7zz", "7z"}, Dirs: []string{`C:\Program Files\7-Zip`, `D:\Program Files\7-Zip`}, Purpose: "archive extraction"},
	{Name: "adb", Class: "dynamic", Exes: []string{"adb.exe", "adb"}, Version: []string{"version"}, Purpose: "Android device bridge"},
	{Name: "frida", Class: "dynamic", Exes: []string{"frida.exe", "frida"}, Version: []string{"--version"}, Purpose: "dynamic instrumentation"},
	{Name: "frida-ps", Class: "dynamic", Exes: []string{"frida-ps.exe", "frida-ps"}, Version: []string{"--version"}, Purpose: "list processes via frida"},
}

// ToolBox resolves and runs catalog tools.
type ToolBox struct {
	Home         string
	HomeFunc     func() string
	AllowDynamic bool
	overrides    map[string]string
}

// NewToolBox loads <home>/tools.json ({"jadx":"D:\\x\\jadx.bat"}) and RE_TOOL_<NAME> overrides.
func NewToolBox(home string, allowDynamic bool) *ToolBox {
	tb := &ToolBox{Home: home, AllowDynamic: allowDynamic, overrides: map[string]string{}}
	if b, err := os.ReadFile(filepath.Join(home, "tools.json")); err == nil {
		_ = json.Unmarshal(b, &tb.overrides)
	}
	return tb
}

func (tb *ToolBox) homeDir() string {
	if tb.HomeFunc != nil {
		if h := tb.HomeFunc(); h != "" {
			return h
		}
	}
	return tb.Home
}

func envKey(name string) string {
	return "RE_TOOL_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
}

func (tb *ToolBox) resolve(s ToolSpec) string {
	if p := os.Getenv(envKey(s.Name)); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p := tb.overrides[s.Name]; p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if h := tb.homeDir(); h != "" && h != tb.Home {
		var dyn map[string]string
		if b, err := os.ReadFile(filepath.Join(h, "tools.json")); err == nil && json.Unmarshal(b, &dyn) == nil {
			if p := dyn[s.Name]; p != "" {
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
		}
	}
	for _, d := range s.Dirs {
		for _, x := range s.Exes {
			p := filepath.Join(d, x)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	for _, x := range s.Exes {
		if p, err := exec.LookPath(x); err == nil {
			return p
		}
	}
	return ""
}

// Probe lists every catalog tool with its resolved path (and version when asked).
func (tb *ToolBox) Probe(withVersions bool) []ToolSpec {
	out := make([]ToolSpec, 0, len(catalog))
	for _, s := range catalog {
		s.Resolved = tb.resolve(s)
		out = append(out, s)
	}
	return out
}

// Version runs the tool's version command with a short timeout.
func (tb *ToolBox) Version(s ToolSpec) string {
	if s.Resolved == "" || len(s.Version) == 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Resolved, s.Version...)
	processutil.HideWindow(cmd)
	cmd.Env = append(os.Environ(), s.Env...)
	b, _ := cmd.CombinedOutput()
	line := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	if len(line) > 120 {
		line = line[:120]
	}
	return line
}

// RunResult is the outcome of one launch.
type RunResult struct {
	Tool      string   `json:"tool"`
	Exe       string   `json:"exe"`
	Args      []string `json:"args"`
	ExitCode  int      `json:"exit_code"`
	TimedOut  bool     `json:"timed_out"`
	Seconds   float64  `json:"seconds"`
	Output    string   `json:"output"`
	Truncated bool     `json:"truncated"`
	SpillPath string   `json:"spill_path"`
	TotalSize int64    `json:"total_output_bytes"`
}

// Run launches an allowlisted tool with an argv array (no shell), capturing combined
// output into a spill file and returning a head+tail excerpt of at most maxChars.
func (tb *ToolBox) Run(c, name string, args []string, cwd string, timeout time.Duration, maxChars int) (*RunResult, error) {
	var spec *ToolSpec
	for i := range catalog {
		if catalog[i].Name == name {
			spec = &catalog[i]
		}
	}
	if spec == nil {
		return nil, fmt.Errorf("tool %q is not in the allowlist; call re_env to see names", name)
	}
	if spec.Class == "dynamic" && !tb.AllowDynamic {
		return nil, fmt.Errorf("tool %q is dynamic (touches devices/processes): start agentgo-re with --allow-dynamic or AGENTGO_RE_ALLOW_DYNAMIC=1", name)
	}
	exe := tb.resolve(*spec)
	home := tb.homeDir()
	if exe == "" {
		return nil, fmt.Errorf("tool %q not found: install it, put it on PATH, set %s, or add it to %s", name, envKey(name), filepath.Join(home, "tools.json"))
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if timeout > 30*time.Minute {
		timeout = 30 * time.Minute
	}
	if maxChars <= 0 {
		maxChars = 8000
	}
	d, err := caseDir(home, c)
	if err != nil {
		return nil, err
	}
	spillDir := filepath.Join(d, "spill")
	if err := os.MkdirAll(spillDir, 0o755); err != nil {
		return nil, err
	}
	spill := filepath.Join(spillDir, fmt.Sprintf("%s-%s.log", time.Now().Format("20060102-150405"), name))
	out, err := os.Create(spill)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	processutil.HideWindow(cmd)
	cmd.Dir = cwd
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Env = append(append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1"), spec.Env...)
	start := time.Now()
	runErr := cmd.Run()
	out.Close()
	res := &RunResult{Tool: name, Exe: exe, Args: args, Seconds: round2(time.Since(start).Seconds()), SpillPath: spill}
	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
	}
	if ee, ok := runErr.(*exec.ExitError); ok {
		res.ExitCode = ee.ExitCode()
	} else if runErr != nil && !res.TimedOut {
		return nil, fmt.Errorf("launch failed: %w (os=%s)", runErr, runtime.GOOS)
	}
	data, _ := os.ReadFile(spill)
	res.TotalSize = int64(len(data))
	text := strings.ToValidUTF8(string(data), "?")
	if len(text) > maxChars {
		head := maxChars * 6 / 10
		tail := maxChars - head
		text = text[:head] + fmt.Sprintf("\n…[%d bytes omitted; full output in spill_path]…\n", len(text)-maxChars) + text[len(text)-tail:]
		res.Truncated = true
	}
	res.Output = strings.ToValidUTF8(text, "?")
	return res, nil
}
