package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agentgo/internal/skills"
)

// 写一个会打印密钥并写文件的脚本，Windows 用 .cmd，其他系统用 .sh。
func skillWithScript(t *testing.T, credentials string) (*skills.Loader, string) {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, "skills", "demo")
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: demo\ndescription: demo skill for tests\ncredentials: [" + credentials + "]\n---\n# demo\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	name, body := "scripts/run.sh", "echo token=$DEMO_TOKEN\necho made > \"$AGENTGO_SKILL_OUTPUT_DIR/out.txt\"\nexit 0\n"
	if runtime.GOOS == "windows" {
		name = "scripts/run.cmd"
		body = "@echo off\r\necho token=%DEMO_TOKEN%\r\necho made> \"%AGENTGO_SKILL_OUTPUT_DIR%\\out.txt\"\r\nexit /b 0\r\n"
	} else if _, ok := skills.LookBin("bash"); !ok {
		t.Skip("bash not available")
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	l := skills.NewLoader(ws)
	l.Reload()
	return l, name
}

func TestRunSkillScriptGivesTheScriptTheCredentialButNeverTheModel(t *testing.T) {
	l, script := skillWithScript(t, "DEMO_TOKEN")
	credFile := filepath.Join(t.TempDir(), "c.json")
	_ = os.WriteFile(credFile, []byte(`{"DEMO_TOKEN":"s3cr3t-value"}`), 0o600)
	l.SetCredentialFile(credFile)

	out := runSkillScript(context.Background(), l, runSkillScriptInput{SkillID: "skill:demo", Script: script})
	if !out.Success || out.Error != "" {
		t.Fatalf("out = %+v", out)
	}
	if strings.Contains(out.Stdout, "s3cr3t-value") || !strings.Contains(out.Stdout, "token=***") {
		t.Fatalf("the script saw the credential but the model must get it scrubbed: %q", out.Stdout)
	}
	if len(out.OutputFiles) != 1 || out.OutputFiles[0] != "out.txt" {
		t.Fatalf("output files = %v", out.OutputFiles)
	}
}

func TestRunSkillScriptRefusesWhenACredentialIsMissing(t *testing.T) {
	l, script := skillWithScript(t, "DEMO_TOKEN_NOT_SET_ANYWHERE")
	out := runSkillScript(context.Background(), l, runSkillScriptInput{SkillID: "demo", Script: script})
	if out.Success || !strings.Contains(out.Error, "DEMO_TOKEN_NOT_SET_ANYWHERE") {
		t.Fatalf("out = %+v", out)
	}
}

func TestRunSkillScriptStaysInsideTheSkillFolder(t *testing.T) {
	l, _ := skillWithScript(t, "")
	for _, bad := range []string{"../../outside.sh", "/etc/passwd", `C:\Windows\system32\cmd.exe`, "", "scripts/nope.sh", "scripts"} {
		out := runSkillScript(context.Background(), l, runSkillScriptInput{SkillID: "skill:demo", Script: bad})
		if out.Success || out.Error == "" {
			t.Errorf("%q must be refused: %+v", bad, out)
		}
	}
	if out := runSkillScript(context.Background(), l, runSkillScriptInput{SkillID: "skill:office", Script: "x.py"}); out.Success {
		t.Errorf("built-in skills have no scripts: %+v", out)
	}
}

func TestActivateSkillReportsReadinessAndResources(t *testing.T) {
	l, _ := skillWithScript(t, "DEMO_TOKEN_NOT_SET_ANYWHERE")
	r := NewRegistry()
	if err := RegisterActivateSkill(r, l); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, bt := range r.GetAllTools() {
		if info, err := bt.Info(context.Background()); err == nil {
			names = append(names, info.Name)
		}
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "activate_skill") || !strings.Contains(joined, "run_skill_script") {
		t.Fatalf("tools = %v", names)
	}
}
