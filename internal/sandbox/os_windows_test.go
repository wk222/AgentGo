//go:build windows

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"agentgo/internal/shellcmd"
)

// These tests run real confined commands. They need an NTFS folder the current
// user may label (folders under the user's profile qualify); where labelling is
// not allowed they skip rather than fail.

// workspace returns a fresh empty folder under the user's cache directory. It is
// not t.TempDir(): `go test` points TEMP at GOTMPDIR, and a disk outside the
// profile usually does not let the user label folders.
func workspace(t *testing.T) string {
	t.Helper()
	base, err := os.UserCacheDir()
	if err != nil {
		t.Skip(err)
	}
	root := filepath.Join(base, "agentgo", "sandbox-tests")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
		os.Remove(root) // only if now empty
	})
	return dir
}

// runWin runs line confined in ws the way execute_bash does: through Confine and
// shellcmd.Run. The network is acknowledged, as Windows requires.
func runWin(t *testing.T, ws, line string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd, err := Confine(ctx, line, ws, nil, true)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Skipf("cannot label folders here: %v", err)
		}
		t.Fatalf("confine: %v", err)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	code, err := shellcmd.Run(cmd, shellcmd.TreeOptions{})
	if err != nil {
		t.Fatalf("run %q: %v\n%s", line, err, out.String())
	}
	return out.String(), code
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return strings.TrimSpace(string(b))
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestWindowsRefusesWithoutNetworkAcknowledgement(t *testing.T) {
	_, err := Confine(context.Background(), "echo hi", workspace(t), nil, false)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
	if !strings.Contains(err.Error(), "AGENTGO_SANDBOX_OS_NET") {
		t.Fatalf("the error must say how to proceed: %v", err)
	}
}

func TestWindowsFirstUseLabelsTheWorkspace(t *testing.T) {
	ws := workspace(t)
	writeFile(t, filepath.Join(ws, "before.txt"), "old")
	if hasLowLabel(ws) {
		t.Fatal("a fresh folder must not be labelled yet")
	}
	// The file existed before the label: it must be writable too.
	if out, code := runWin(t, ws, `echo changed> before.txt`); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !hasLowLabel(ws) {
		t.Fatal("the workspace must carry the Low label after the first confined command")
	}
	if got := fileText(filepath.Join(ws, "before.txt")); got != "changed" {
		t.Fatalf("before.txt = %q", got)
	}
}

func TestWindowsWritesStayInsideTheWorkspace(t *testing.T) {
	ws := workspace(t)
	outside := workspace(t) // a sibling: Medium integrity, never labelled
	writeFile(t, filepath.Join(ws, "existing.txt"), "old")

	// Everything a build or an edit does inside the workspace has to work,
	// including from a grandchild process.
	line := `echo changed> existing.txt && echo new> created.txt && mkdir sub\deep && echo x> sub\deep\f.txt` +
		` && cmd /C "echo viaChild> grandchild.txt" && del existing.txt`
	if out, code := runWin(t, ws, line); code != 0 {
		t.Fatalf("inside writes failed, exit %d: %s", code, out)
	}
	for path, want := range map[string]string{
		filepath.Join(ws, "created.txt"):          "new",
		filepath.Join(ws, "sub", "deep", "f.txt"): "x",
		filepath.Join(ws, "grandchild.txt"):       "viaChild",
	} {
		if got := fileText(path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if exists(filepath.Join(ws, "existing.txt")) {
		t.Error("existing.txt should have been deleted inside the workspace")
	}

	// The same three changes, tried in the workspace and outside it. Success is
	// judged by effect, not by exit code (del reports "Access is denied" yet exits
	// 0) or by message text (it is localised). Inside, each must take effect, which
	// also shows the command line itself is sound; outside, none may.
	for _, a := range []struct {
		name string
		line func(dir string) string
		done func(dir string) bool // did the change take effect in dir?
	}{
		{"overwrite", func(d string) string { return `echo hacked> "` + filepath.Join(d, "victim.txt") + `"` },
			func(d string) bool { return fileText(filepath.Join(d, "victim.txt")) == "hacked" }},
		{"create", func(d string) string { return `echo hacked> "` + filepath.Join(d, "new.txt") + `"` },
			func(d string) bool { return exists(filepath.Join(d, "new.txt")) }},
		{"delete", func(d string) string { return `del "` + filepath.Join(d, "gone.txt") + `"` },
			func(d string) bool { return !exists(filepath.Join(d, "gone.txt")) }},
	} {
		for _, dir := range []string{ws, outside} {
			writeFile(t, filepath.Join(dir, "victim.txt"), "keep")
			writeFile(t, filepath.Join(dir, "gone.txt"), "keep")
			os.Remove(filepath.Join(dir, "new.txt"))
			runWin(t, ws, a.line(dir))
			where := "the workspace"
			if dir == outside {
				where = "a folder outside it"
			}
			if got, want := a.done(dir), dir == ws; got != want {
				t.Errorf("%s in %s: took effect = %v, want %v", a.name, where, got, want)
			}
		}
	}

	// The shared temp folder is closed too: the sandbox has a private one.
	sharedTemp := filepath.Join(os.TempDir(), fmt.Sprintf("agentgo-sbx-%d.txt", time.Now().UnixNano()))
	t.Cleanup(func() { os.Remove(sharedTemp) })
	runWin(t, ws, `echo hacked> "`+sharedTemp+`"`)
	if exists(sharedTemp) {
		t.Error("a file was created in the shared temp folder")
	}
}

func TestWindowsRegistryWritesAreRefused(t *testing.T) {
	const key = `HKCU\Software\AgentGoSandboxTest`
	del := func() { exec.Command("reg", "delete", key, "/f").Run() }
	del()
	t.Cleanup(del)

	// Control: the same command works unconfined, so a refusal below is the sandbox's.
	if out, err := exec.Command("reg", "add", key, "/v", "x", "/t", "REG_SZ", "/d", "1", "/f").CombinedOutput(); err != nil {
		t.Skipf("reg add is not usable here: %v %s", err, out)
	}
	del()

	if _, code := runWin(t, workspace(t), `reg add `+key+` /v x /t REG_SZ /d 1 /f`); code == 0 {
		t.Fatal("a confined command wrote to HKCU")
	}
	if exec.Command("reg", "query", key).Run() == nil {
		t.Fatal("the registry key exists although the command was refused")
	}
}

func TestWindowsTempIsPrivate(t *testing.T) {
	ws := workspace(t)
	private, err := privateTempDir()
	if err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(private, "probe.txt")
	t.Cleanup(func() { os.Remove(probe) })

	out, code := runWin(t, ws, `echo %TEMP%& echo hello> "%TEMP%\probe.txt"`)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, private) {
		t.Fatalf("TEMP was not redirected to %s: %s", private, out)
	}
	if got := fileText(probe); got != "hello" {
		t.Fatalf("probe = %q", got)
	}
}

func TestWindowsTokenIsLowIntegrityWithoutPrivileges(t *testing.T) {
	out, code := runWin(t, workspace(t), `whoami /groups & whoami /priv`)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, `Mandatory Label\Low Mandatory Level`) {
		t.Errorf("not running at Low integrity:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && strings.HasPrefix(f[0], "Se") && strings.HasSuffix(f[0], "Privilege") && f[0] != "SeChangeNotifyPrivilege" {
			t.Errorf("unexpected privilege kept: %s", strings.TrimSpace(line))
		}
	}
}

func TestWindowsConfinedCommandStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd, err := Confine(ctx, `ping -n 30 127.0.0.1 >nul`, workspace(t), nil, true)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Skipf("cannot label folders here: %v", err)
		}
		t.Fatal(err)
	}
	start := time.Now()
	code, err := shellcmd.Run(cmd, shellcmd.TreeOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatalf("the command ended on its own (exit %d) before the deadline; the test proves nothing", code)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("stopping took %s", d)
	}
}

func TestWindowsLabelDoesNotFollowJunctions(t *testing.T) {
	outside := workspace(t)
	protected := filepath.Join(outside, "protected.txt")
	writeFile(t, protected, "keep")

	ws := workspace(t)
	link := filepath.Join(ws, "link")
	if out, err := exec.Command("cmd", "/C", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction: %v %s", err, out)
	}
	t.Cleanup(func() { os.Remove(link) }) // remove the link itself, never what it points at

	out, code := runWin(t, ws, `echo hacked> link\protected.txt`)
	if hasLowLabel(outside) {
		t.Fatal("the Low label was pushed through a junction onto a folder outside the workspace")
	}
	if code == 0 {
		t.Fatalf("a write through a junction out of the workspace succeeded: %s", out)
	}
	if got := fileText(protected); got != "keep" {
		t.Fatalf("protected.txt = %q", got)
	}
}

func TestWindowsLabelFailureRefusesTheCommand(t *testing.T) {
	ws := workspace(t)
	// Deny "take ownership" to everyone: labelling then fails, as on a folder
	// the user does not own.
	if out, err := exec.Command("icacls", ws, "/deny", "*S-1-1-0:(WO)").CombinedOutput(); err != nil {
		t.Skipf("icacls is not usable here: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("icacls", ws, "/remove:d", "*S-1-1-0").Run() })

	_, err := Confine(context.Background(), "echo hi", ws, nil, true)
	if err == nil {
		t.Skip("labelling succeeded despite the deny (a privileged environment)")
	}
	if !strings.Contains(err.Error(), "take ownership") {
		t.Fatalf("the error should explain the missing permission: %v", err)
	}
	if hasLowLabel(ws) {
		t.Fatal("the folder is labelled although labelling reported failure")
	}
}

func TestWindowsRefusesHugeFolders(t *testing.T) {
	ws := workspace(t)
	for i := 0; i < 8; i++ {
		writeFile(t, filepath.Join(ws, fmt.Sprintf("f%d.txt", i)), "x")
	}
	old := maxLabelObjects
	maxLabelObjects = 5
	t.Cleanup(func() { maxLabelObjects = old })

	err := ensureLowLabel(ws)
	if err == nil || !strings.Contains(err.Error(), "smaller workspace folder") {
		t.Fatalf("want a refusal asking for a smaller folder, got %v", err)
	}
	if hasLowLabel(ws) {
		t.Fatal("a refused folder must not be labelled")
	}
}

func TestWindowsRefusesRelativeRoots(t *testing.T) {
	if err := ensureLowLabel("relative\\dir"); err == nil {
		t.Fatal("a relative write root must be refused")
	}
}

// tooWide is pure string logic, so it is tested on the dangerous paths
// themselves; ensureLowLabel is deliberately never pointed at them.
func TestWindowsTooWide(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	sysRoot := os.Getenv("SystemRoot")
	for _, tc := range []struct{ dir, want string }{
		{filepath.VolumeName(home) + `\`, "root of a drive"},
		{filepath.VolumeName(home), "root of a drive"},
		{filepath.Dir(home), "user profile"},
		{home, "user profile"},
		{sysRoot, "system folder"},
		{filepath.Join(sysRoot, "System32"), "system folder"},
		{filepath.Join(sysRoot, "Temp"), "system folder"},
	} {
		if got := tooWide(tc.dir); !strings.Contains(got, tc.want) {
			t.Errorf("tooWide(%q) = %q, want it to mention %q", tc.dir, got, tc.want)
		}
	}
	for _, dir := range []string{
		filepath.Join(home, "Documents", "GitHub", "project"),
		filepath.Join(home, "AppData", "Local", "agentgo", "sandbox-tmp"),
		`D:\work\project`,
	} {
		if got := tooWide(dir); got != "" {
			t.Errorf("tooWide(%q) = %q, want it allowed", dir, got)
		}
	}
}

func TestWindowsWithin(t *testing.T) {
	for _, tc := range []struct {
		path, root string
		want       bool
	}{
		{`C:\Users\alice`, `C:\Users\alice`, true},
		{`c:\users\ALICE\Docs`, `C:\Users\alice`, true},
		{`C:\Users\alice2`, `C:\Users\alice`, false},
		{`C:\Users`, `C:\Users\alice`, false},
		{`C:\Users\alice`, `C:\`, true},
	} {
		if got := within(tc.path, tc.root); got != tc.want {
			t.Errorf("within(%q, %q) = %v, want %v", tc.path, tc.root, got, tc.want)
		}
	}
}

func TestWindowsWithTempReplacesBothSpellings(t *testing.T) {
	got := withTemp([]string{"Path=x", "temp=old", "TMP=old", "KEEP=1"}, `C:\sb`)
	want := []string{"Path=x", "KEEP=1", `TEMP=C:\sb`, `TMP=C:\sb`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}
