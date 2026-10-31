package hostui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPSQuoteEscapesSingleQuotes(t *testing.T) {
	if got := psQuote(`C:\Users\O'Brien`); got != `'C:\Users\O''Brien'` {
		t.Fatalf("got %s", got)
	}
}

func TestEncodePSIsUTF16LE(t *testing.T) {
	// "A中" -> 41 00 2D 4E -> base64
	if got := encodePS("A中"); got != "QQAtTg==" {
		t.Fatalf("got %s", got)
	}
}

// The dialog script must at least parse; it cannot be shown in a test.
func TestFolderScriptParses(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("no powershell")
	}
	script := folderScript("选择工作区文件夹", `C:\Users\O'Brien\项目`)
	file := filepath.Join(t.TempDir(), "dialog.ps1")
	if err := os.WriteFile(file, []byte("\ufeff"+script), 0o600); err != nil {
		t.Fatal(err)
	}
	check := `$e=$null;$t=$null;[void][System.Management.Automation.Language.Parser]::ParseFile('` + file + `',[ref]$t,[ref]$e);if($e.Count -gt 0){$e|ForEach-Object{$_.Message};exit 1}`
	out, err := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", check).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Language") && strings.Contains(string(out), "Unable to find type") {
			t.Skip("PowerShell 2: no parser API")
		}
		t.Fatalf("script does not parse: %v\n%s", err, out)
	}
}

func TestFindBrowserNeverFails(t *testing.T) {
	b := FindBrowser()
	if b.Name == "" {
		t.Fatal("FindBrowser must always name a browser or \"default\"")
	}
	if b.Name == "default" && (b.Path != "" || b.App) {
		t.Fatalf("default browser has no path: %+v", b)
	}
}
