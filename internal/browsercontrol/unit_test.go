package browsercontrol

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNamesAgreeOnlyWhenTheyCouldBeTheSameElement(t *testing.T) {
	yes := [][2]string{
		{"Save", "Save"}, {"save", "  SAVE "}, {"Save draft", "Save"}, {"Save", "Save draft (3)"},
		{"提交", "提交申请"}, {"提交申请", "提交"}, {"Search…", "Search"},
		{"anything the model likes", ""}, // a nameless element cannot be checked
	}
	no := [][2]string{
		{"Save", "Delete"}, {"a", "Delete"}, {"Delete", "Undelete"}, {"提交", "撤回"}, {"", "Save"}, {"ok", "Book"},
		{"Cancel", "Cancellation fee"},
	}
	for _, p := range yes {
		if !sameName(p[0], p[1]) {
			t.Errorf("%q vs %q should agree", p[0], p[1])
		}
	}
	for _, p := range no {
		if sameName(p[0], p[1]) {
			t.Errorf("%q vs %q must not agree", p[0], p[1])
		}
	}
}

func TestSnapshotsNeverShowPasswordsAndMarkStateClearly(t *testing.T) {
	yes, no := true, false
	s := snapshot{
		Title: "Login", URL: "https://x.example/login", ScrollY: 0, ScrollHeight: 2000, ViewHeight: 800, TotalElements: 9,
		Elements: []snapElement{
			{Ref: 0, Role: "textbox", Name: "User", Type: "text", Value: "alice", InView: true},
			{Ref: 1, Role: "textbox", Name: "Password", Type: "password", Secret: true, InView: true},
			{Ref: 2, Role: "checkbox", Name: "Remember", Checked: &no, InView: true},
			{Ref: 3, Role: "button", Name: "Go", Disabled: true, InView: false},
			{Ref: 4, Role: "combobox", Name: "Lang", Value: "EN", Options: []string{"EN", "ZH"}, OptionCount: 30, InView: true},
			{Ref: 5, Role: "button", Name: "Menu", Expanded: &yes, InView: true},
		},
		Text: "Welcome", TextTotal: 5000,
	}
	r := s.render()
	for _, want := range []string{
		`[0] textbox "User" value="alice"`, `[1] textbox(password) "Password" [password field`, `[2] checkbox "Remember" [unchecked]`,
		`[3] button "Go" [disabled] [off screen]`, `options: EN | ZH | … (30 options)`, `[expanded]`,
		"Scroll: 0 of 2000 px", "(6 of 9 elements shown", "(text cut at 7 of 5000 characters)",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("render lacks %q:\n%s", want, r)
		}
	}
	// A value that sits in a secret field is never printed, even if the page script sent one.
	s.Elements[1].Value = "hunter2"
	if strings.Contains(s.render(), "hunter2") {
		t.Fatal("a password value reached the model")
	}
	if _, err := parseSnapshot("not json"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestKeySpecs(t *testing.T) {
	enter, err := parseKey("Enter")
	if err != nil || enter.vk != 13 || enter.text != "\r" {
		t.Fatalf("%+v %v", enter, err)
	}
	a, err := parseKey("a")
	if err != nil || a.text != "a" || a.code != "KeyA" || a.vk != 'A' {
		t.Fatalf("%+v %v", a, err)
	}
	ctrlA, err := parseKey("Control+A")
	if err != nil || ctrlA.modifiers != 2 || ctrlA.text != "" || len(ctrlA.commands) != 1 || ctrlA.commands[0] != "selectAll" {
		t.Fatalf("%+v %v", ctrlA, err)
	}
	shiftTab, err := parseKey("shift+tab")
	if err != nil || shiftTab.modifiers != 8 || shiftTab.vk != 9 {
		t.Fatalf("%+v %v", shiftTab, err)
	}
	if k, err := parseKey("5"); err != nil || k.code != "Digit5" {
		t.Fatalf("%+v %v", k, err)
	}
	if k, err := parseKey("Shift++"); err != nil || k.key != "+" || k.modifiers != 8 {
		t.Fatalf("%+v %v", k, err)
	}
	for _, bad := range []string{"", "Hyper+Z", "Enter+", "NotAKey", "Control+"} {
		if _, err := parseKey(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFindBrowser(t *testing.T) {
	none := func(string) (string, error) { return "", errors.New("not found") }
	env := func(k string) string {
		return map[string]string{"ProgramFiles": `C:\PF`, "ProgramFiles(x86)": `C:\PF86`, "LocalAppData": `C:\Local`}[k]
	}
	edge := filepath.Join(`C:\PF86`, "Microsoft", "Edge", "Application", "msedge.exe")
	chrome := filepath.Join(`C:\PF`, "Google", "Chrome", "Application", "chrome.exe")
	has := func(set ...string) func(string) bool {
		return func(p string) bool {
			for _, s := range set {
				if s == p {
					return true
				}
			}
			return false
		}
	}
	// A configured path wins and is reported missing when it is.
	if p, ok := findBrowser(`D:\mine\chrome.exe`, "windows", env, has(`D:\mine\chrome.exe`, edge), none); !ok || p != `D:\mine\chrome.exe` {
		t.Fatalf("%q %v", p, ok)
	}
	if _, ok := findBrowser(`D:\mine\chrome.exe`, "windows", env, has(edge), none); ok {
		t.Fatal("a configured path that does not exist was replaced by another browser")
	}
	// Otherwise the usual places, Chrome before Edge within a root.
	if p, ok := findBrowser("", "windows", env, has(edge), none); !ok || p != edge {
		t.Fatalf("%q %v", p, ok)
	}
	if p, ok := findBrowser("", "windows", env, has(edge, chrome), none); !ok || p != chrome {
		t.Fatalf("%q %v", p, ok)
	}
	if _, ok := findBrowser("", "windows", env, has(), none); ok {
		t.Fatal("found a browser on an empty machine")
	}
	look := func(name string) (string, error) {
		if name == "chromium" {
			return "/usr/bin/chromium", nil
		}
		return "", errors.New("no")
	}
	if p, ok := findBrowser("", "linux", env, has(), look); !ok || p != "/usr/bin/chromium" {
		t.Fatalf("%q %v", p, ok)
	}
}

func TestLaunchArgsKeepTheProfileSeparateAndQuiet(t *testing.T) {
	args := strings.Join(launchArgs(`C:\data\browser\profile`, true, []string{"--no-sandbox"}), " ")
	for _, want := range []string{"--remote-debugging-port=0", `--user-data-dir=C:\data\browser\profile`, "--disable-background-networking", "--headless", "--no-sandbox"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %s in %s", want, args)
		}
	}
	if !strings.HasSuffix(args, "about:blank") {
		t.Errorf("the browser must start on a blank page: %s", args)
	}
	if strings.Contains(strings.Join(launchArgs("p", false, nil), " "), "--headless") {
		t.Error("headless requested when not asked for")
	}
}

func TestDevToolsActivePortFile(t *testing.T) {
	dir := t.TempDir()
	if _, ok := readActivePort(dir); ok {
		t.Fatal("no file, yet an address")
	}
	write := func(s string) { _ = os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(s), 0o644) }
	write("61773\n/devtools/browser/cdf0bd3f-db8a-4c9f-8b4a-c79c11141a34\n")
	if ws, ok := readActivePort(dir); !ok || ws != "ws://127.0.0.1:61773/devtools/browser/cdf0bd3f-db8a-4c9f-8b4a-c79c11141a34" {
		t.Fatalf("%q %v", ws, ok)
	}
	// Half-written or wrong files are not trusted: the address is always local.
	for _, bad := range []string{"", "61773", "61773\n", "x\n/devtools/browser/a", "0\n/devtools/browser/a", "99999\n/devtools/browser/a", "61773\n/other/path", "61773\nhttp://evil/devtools/browser/a"} {
		write(bad)
		if ws, ok := readActivePort(dir); ok {
			t.Errorf("%q accepted as %q", bad, ws)
		}
	}
}
