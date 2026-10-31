package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/config"
	"agentgo/internal/processutil"
	"agentgo/internal/shellcmd"
	"agentgo/internal/skills"
)

// run_skill_script runs one script that ships with a workspace skill. It exists so a skill can use
// a secret without the model ever seeing it: the skill names the credential, this tool looks the value up
// and puts it in that one script's environment, and the output is scrubbed of it before it comes back.
// Everything else (process tree, time limit, output cap) is the same machinery execute_bash uses.

type runSkillScriptInput struct {
	SkillID        string   `json:"skill_id" jsonschema:"description=The skill that owns the script\\, e.g. skill:weekly-report"`
	Script         string   `json:"script" jsonschema:"description=Path of the script inside the skill folder\\, as listed in activate_skill resources\\, e.g. scripts/build.py"`
	Args           []string `json:"args,omitempty" jsonschema:"description=Arguments passed to the script as separate words"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty" jsonschema:"description=Time limit in seconds (default and maximum follow execute_bash)"`
}

type runSkillScriptOutput struct {
	Success     bool     `json:"success"`
	ExitCode    int      `json:"exit_code"`
	Stdout      string   `json:"stdout,omitempty"`
	Stderr      string   `json:"stderr,omitempty"`
	OutputDir   string   `json:"output_dir,omitempty"`
	OutputFiles []string `json:"output_files,omitempty"`
	Error       string   `json:"error,omitempty"`
}

func describeMissing(r skills.Readiness) string {
	var parts []string
	if len(r.MissingBins) > 0 {
		parts = append(parts, "programs not found on PATH: "+strings.Join(r.MissingBins, ", "))
	}
	if len(r.MissingCredentials) > 0 {
		parts = append(parts, "credentials not set: "+strings.Join(r.MissingCredentials, ", "))
	}
	return strings.Join(parts, "; ")
}

var scriptExts = map[string]bool{".py": true, ".ps1": true, ".js": true, ".mjs": true, ".sh": true, ".bat": true, ".cmd": true}

func hasScript(files []string) bool {
	for _, f := range files {
		if scriptExts[strings.ToLower(filepath.Ext(f))] {
			return true
		}
	}
	return false
}

// interpreterFor returns the program and leading arguments that run a script of this type.
func interpreterFor(script string) (string, []string, error) {
	ext := strings.ToLower(filepath.Ext(script))
	need := func(bin string, lead ...string) (string, []string, error) {
		p, ok := skills.LookBin(bin)
		if !ok {
			return "", nil, fmt.Errorf("%s is needed to run %s but was not found on PATH", bin, filepath.Base(script))
		}
		return p, lead, nil
	}
	switch ext {
	case ".py":
		return need("python")
	case ".ps1":
		return need("pwsh", "-NoProfile", "-NonInteractive", "-File")
	case ".js", ".mjs":
		return need("node")
	case ".sh":
		return need("bash")
	case ".bat", ".cmd":
		if runtime.GOOS != "windows" {
			return "", nil, fmt.Errorf("%s scripts only run on Windows", ext)
		}
		return "cmd.exe", []string{"/c"}, nil
	}
	return "", nil, fmt.Errorf("unsupported script type %q (supported: .py .ps1 .js .mjs .sh .bat .cmd)", ext)
}

// resolveScript maps a script name to a regular file inside the skill folder, refusing anything that
// leaves it (".." or a symlink pointing elsewhere).
func resolveScript(skillDir, script string) (string, error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return "", fmt.Errorf("script is required")
	}
	if filepath.IsAbs(script) {
		return "", fmt.Errorf("script must be a path inside the skill folder, not an absolute path")
	}
	full := filepath.Join(skillDir, filepath.FromSlash(script))
	realDir, err := filepath.EvalSymlinks(skillDir)
	if err != nil {
		return "", err
	}
	realFull, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", fmt.Errorf("script %q not found in the skill folder", script)
	}
	rel, err := filepath.Rel(realDir, realFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("script %q is outside the skill folder", script)
	}
	st, err := os.Stat(realFull)
	if err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("script %q is not a file", script)
	}
	return realFull, nil
}

func registerRunSkillScript(r *Registry, loader *skills.Loader) error {
	t, err := utils.InferTool("run_skill_script",
		"Run a script that ships with a workspace skill (see resources in activate_skill). Checks the skill's requirements first, "+
			"gives the script the credentials the skill declares (you never see their values), and returns stdout, stderr and the files "+
			"the script wrote to the output folder named in AGENTGO_SKILL_OUTPUT_DIR. Interpreters: .py .ps1 .js .sh .bat .cmd. "+
			shellTimeoutNote(),
		func(ctx context.Context, in runSkillScriptInput) (runSkillScriptOutput, error) {
			return runSkillScript(ctx, loader, in), nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

func runSkillScript(ctx context.Context, loader *skills.Loader, in runSkillScriptInput) runSkillScriptOutput {
	fail := func(format string, a ...any) runSkillScriptOutput {
		return runSkillScriptOutput{Error: fmt.Sprintf(format, a...), ExitCode: -1}
	}
	id := strings.TrimSpace(in.SkillID)
	sk, ok := loader.Get(id)
	if !ok {
		sk, ok = loader.Get("skill:" + strings.TrimPrefix(id, "skill:"))
	}
	if !ok {
		loader.Reload()
		if sk, ok = loader.Get("skill:" + strings.TrimPrefix(id, "skill:")); !ok {
			return fail("skill not found: %s", id)
		}
	}
	if sk.Dir == "" {
		return fail("%s is built into AgentGo and has no scripts", sk.ID)
	}
	if config.SandboxOS.On() {
		return fail("AGENTGO_SANDBOX_OS is on and this tool cannot be confined yet; run the script with execute_bash instead")
	}
	script, err := resolveScript(sk.Dir, in.Script)
	if err != nil {
		return fail("%v", err)
	}
	if rd := loader.Check(sk); !rd.OK {
		return fail("%s cannot run yet: %s", sk.ID, describeMissing(rd))
	}
	prog, lead, err := interpreterFor(script)
	if err != nil {
		return fail("%v", err)
	}

	outDir := filepath.Join(loader.Root(), ".agentgo", "skill-output", safeName(sk.Name))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fail("cannot create the output folder: %v", err)
	}
	before := listFiles(outDir)

	args := append(append([]string{}, lead...), script)
	args = append(args, in.Args...)
	timeout := shellTimeout(in.TimeoutSeconds)
	runCtx, cancel := ctx, context.CancelFunc(func() {})
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, prog, args...)
	processutil.HideWindow(cmd)
	cmd.Dir = loader.Root()
	env := append(os.Environ(), "AGENTGO_SKILL_DIR="+sk.Dir, "AGENTGO_SKILL_OUTPUT_DIR="+outDir, "PYTHONIOENCODING=utf-8")
	var secrets []string
	for _, name := range sk.Credentials {
		if v, ok := loader.Credential(name); ok {
			env = append(env, name+"="+v)
			secrets = append(secrets, v)
		}
	}
	cmd.Env = shellcmd.WithUTF8Env(env)
	stdout, stderr := shellcmd.NewCapture(shellOutputCap), shellcmd.NewCapture(shellOutputCap)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	code, runErr := shellcmd.Run(cmd, shellcmd.TreeOptions{BestEffort: true})
	if ctx.Err() != nil {
		return fail("cancelled")
	}
	timedOut := runCtx.Err() != nil && (runErr != nil || code != 0)
	if runErr != nil && !timedOut {
		return fail("could not run %s: %v", in.Script, runErr)
	}
	out := runSkillScriptOutput{
		ExitCode: code, Success: code == 0 && !timedOut,
		Stdout: scrub(stdout.String(), secrets), Stderr: scrub(stderr.String(), secrets), OutputDir: outDir,
	}
	if timedOut {
		out.ExitCode = shellTimeoutStatus
		out.Error = fmt.Sprintf("the script ran longer than %d seconds and was stopped", int(timeout/time.Second))
	}
	after := listFiles(outDir)
	for name, mod := range after {
		if old, existed := before[name]; !existed || !old.Equal(mod) {
			out.OutputFiles = append(out.OutputFiles, name)
		}
	}
	sort.Strings(out.OutputFiles)
	return out
}

// scrub removes secret values from text that goes back to the model.
func scrub(s string, secrets []string) string {
	for _, v := range secrets {
		if len(v) >= 4 {
			s = strings.ReplaceAll(s, v, "***")
		}
	}
	return s
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r > 127:
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "skill"
	}
	return b.String()
}

func listFiles(dir string) map[string]time.Time {
	out := map[string]time.Time{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, e := d.Info(); e == nil {
			if rel, e2 := filepath.Rel(dir, p); e2 == nil {
				out[filepath.ToSlash(rel)] = info.ModTime()
			}
		}
		return nil
	})
	return out
}
