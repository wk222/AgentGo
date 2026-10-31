// Package hostui holds the few things a browser-hosted AgentGo needs from the
// operating system: opening a browser window and picking a folder. It has no
// third-party dependencies, so it also builds with the Windows 7 toolchain.
package hostui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Browser describes how a page will be shown.
type Browser struct {
	Name string // "chrome", "edge", "firefox" or "default"
	Path string // executable; empty for "default"
	App  bool   // opened in a chromeless --app window
}

// FindBrowser picks the best installed browser. Chromium browsers come first
// because --app gives a window without tabs or an address bar; Firefox is next;
// otherwise the system default is used (on Windows 7 that is often IE11, which
// cannot run the UI — the server shows an upgrade hint in that case).
func FindBrowser() Browser {
	for _, c := range chromiumCandidates() {
		if fileExists(c.path) {
			return Browser{Name: c.name, Path: c.path, App: true}
		}
	}
	for _, p := range firefoxCandidates() {
		if fileExists(p) {
			return Browser{Name: "firefox", Path: p}
		}
	}
	return Browser{Name: "default"}
}

// OpenURL shows url in the best available browser.
func OpenURL(url string) (Browser, error) {
	b := FindBrowser()
	return b, b.Open(url)
}

// Open shows url with this browser.
func (b Browser) Open(url string) error {
	switch {
	case b.Path != "" && b.App:
		cmd := exec.Command(b.Path, "--app="+url, "--new-window")
		hideWindow(cmd)
		return cmd.Start()
	case b.Path != "":
		cmd := exec.Command(b.Path, url)
		hideWindow(cmd)
		return cmd.Start()
	}
	return openDefault(url)
}

func openDefault(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	hideWindow(cmd)
	return cmd.Start()
}

type candidate struct{ name, path string }

func chromiumCandidates() []candidate {
	var out []candidate
	if runtime.GOOS != "windows" {
		for _, n := range []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge"} {
			if p, err := exec.LookPath(n); err == nil {
				out = append(out, candidate{"chrome", p})
			}
		}
		return out
	}
	for _, root := range windowsRoots() {
		out = append(out,
			candidate{"chrome", filepath.Join(root, `Google\Chrome\Application\chrome.exe`)},
			candidate{"edge", filepath.Join(root, `Microsoft\Edge\Application\msedge.exe`)},
		)
	}
	return out
}

func firefoxCandidates() []string {
	if runtime.GOOS != "windows" {
		if p, err := exec.LookPath("firefox"); err == nil {
			return []string{p}
		}
		return nil
	}
	var out []string
	for _, root := range windowsRoots() {
		out = append(out, filepath.Join(root, `Mozilla Firefox\firefox.exe`))
	}
	return out
}

func windowsRoots() []string {
	var roots []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA", "ProgramW6432"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			roots = append(roots, v)
		}
	}
	return roots
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// ErrFolderPickerUnavailable is returned where no native folder dialog exists.
var ErrFolderPickerUnavailable = errors.New("folder picker is not available on this system")
