package browsercontrol

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"agentgo/internal/processutil"
)

// FindBrowser returns the Chrome or Edge executable to drive: the configured
// path when given, otherwise the first one found in the usual places.
func FindBrowser(configured string) (string, bool) {
	return findBrowser(configured, runtime.GOOS, os.Getenv, fileExists, exec.LookPath)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func findBrowser(configured, goos string, env func(string) string, exists func(string) bool, lookPath func(string) (string, error)) (string, bool) {
	if p := strings.TrimSpace(configured); p != "" {
		return p, exists(p)
	}
	var candidates []string
	switch goos {
	case "windows":
		for _, root := range []string{env("ProgramFiles"), env("ProgramFiles(x86)"), env("LocalAppData")} {
			if root == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"))
		}
	case "darwin":
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium")
	}
	for _, c := range candidates {
		if exists(c) {
			return c, true
		}
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "microsoft-edge-stable", "chrome", "msedge"} {
		if p, err := lookPath(name); err == nil {
			return p, true
		}
	}
	return "", false
}

// launchArgs is how the browser is started. The profile directory belongs to
// AgentGo: a person signs in to their systems in that window once and the
// session is kept for the next run, while their everyday browser profile is
// never touched.
func launchArgs(profile string, headless bool, extra []string) []string {
	args := []string{
		"--remote-debugging-port=0",
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		// An intranet PC has no internet; none of these should try to phone home.
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-sync",
		"--disable-default-apps",
		"--no-pings",
		"--disable-features=Translate,OptimizationHints,MediaRouter",
	}
	if headless {
		args = append(args, "--headless", "--hide-scrollbars", "--mute-audio", "--window-size=1366,900")
	}
	args = append(args, extra...)
	return append(args, "about:blank")
}

// readActivePort reads the DevTools address a running browser wrote into its
// profile directory: first line the port, second the browser endpoint path.
func readActivePort(profile string) (string, bool) {
	f, err := os.Open(filepath.Join(profile, "DevToolsActivePort"))
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var lines []string
	for sc.Scan() && len(lines) < 2 {
		lines = append(lines, strings.TrimSpace(sc.Text()))
	}
	if len(lines) < 2 {
		return "", false
	}
	port, err := strconv.Atoi(lines[0])
	if err != nil || port <= 0 || port > 65535 || !strings.HasPrefix(lines[1], "/devtools/browser/") {
		return "", false
	}
	return fmt.Sprintf("ws://127.0.0.1:%d%s", port, lines[1]), true
}

// connect attaches to the browser already running on this profile or starts one.
// owned reports whether this call started it.
func connectBrowser(ctx context.Context, cfg Config) (conn *cdpConn, cmd *exec.Cmd, exited chan struct{}, err error) {
	path, ok := FindBrowser(cfg.BrowserPath)
	if !ok {
		return nil, nil, nil, errors.New("no Chrome or Edge was found on this PC; install one or set AGENTGO_BROWSER_PATH")
	}
	if cfg.ProfileDir == "" {
		return nil, nil, nil, errors.New("no browser profile directory is configured")
	}
	if err := os.MkdirAll(cfg.ProfileDir, 0o755); err != nil {
		return nil, nil, nil, fmt.Errorf("prepare browser profile: %w", err)
	}
	// A browser from an earlier run may still hold this profile.
	if ws, ok := readActivePort(cfg.ProfileDir); ok {
		dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		conn, derr := dialCDP(dctx, ws)
		cancel()
		if derr == nil {
			return conn, nil, nil, nil
		}
	}
	_ = os.Remove(filepath.Join(cfg.ProfileDir, "DevToolsActivePort"))

	cmd = exec.Command(path, launchArgs(cfg.ProfileDir, cfg.Headless, cfg.ExtraArgs)...)
	if cfg.Headless {
		// The browser is a window program and opens no console of its own. Hiding
		// is only applied when no window is wanted, because a hidden start would
		// also hide the window the person is meant to see and share.
		processutil.HideWindow(cmd)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("start the browser: %w", err)
	}
	exited = make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()

	timeout := cfg.StartTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	// Current Edge starts through a launcher that exits at once while the real
	// browser carries on, so the launcher ending is not a failure by itself: it
	// is only one when no debug address shows up shortly after.
	var exitedAt time.Time
	launcherEnded := (<-chan struct{})(exited)
	for {
		if ws, ok := readActivePort(cfg.ProfileDir); ok {
			dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			c, derr := dialCDP(dctx, ws)
			cancel()
			if derr == nil {
				return c, cmd, exited, nil
			}
		}
		if !exitedAt.IsZero() && time.Since(exitedAt) > 8*time.Second {
			return nil, nil, nil, errors.New("the browser exited before it could be controlled; close any window that uses the AgentGo browser profile and try again")
		}
		select {
		case <-launcherEnded:
			exitedAt = time.Now()
			launcherEnded = nil // a nil channel blocks: keep polling without spinning
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, nil, nil, ctx.Err()
		case <-deadline.C:
			_ = cmd.Process.Kill()
			return nil, nil, nil, errors.New("the browser did not become ready in time")
		case <-tick.C:
		}
	}
}
