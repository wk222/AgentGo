package browsercontrol

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests drive a real Chrome or Edge in headless mode against a local
// server. They are skipped when no browser is installed.

const demoPage = `<!doctype html><html><head><meta charset="utf-8"><title>Demo 页面</title></head><body>
<h1>Order form</h1>
<form id="f" action="/submitted" method="get">
  <label for="user">Username</label><input id="user" name="user" type="text">
  <label for="pw">Password</label><input id="pw" name="pw" type="password">
  <label for="country">Country</label>
  <select id="country" name="country"><option value="cn">China</option><option value="us">United States</option><option value="jp">Japan</option></select>
  <label><input id="agree" type="checkbox" name="agree"> I agree</label>
  <input type="submit" value="Send form">
</form>
<button id="greet" onclick="document.getElementById('out').textContent='Hello '+document.getElementById('user').value">Greet</button>
<button id="alerter" onclick="alert('careful now'); document.getElementById('out').textContent='alert closed'">Alert me</button>
<button id="asker" onclick="document.getElementById('out').textContent='confirm='+confirm('really?')">Ask me</button>
<button id="probe" onclick="fetch('http://10.255.255.1/secret').then(function(){document.getElementById('out').textContent='reached'}).catch(function(){document.getElementById('out').textContent='probe failed'})">Probe</button>
<a href="/next">Next page</a>
<a href="/popup" target="_blank">Open popup</a>
<div id="out">idle</div>
<img src="http://169.254.169.254/latest/meta-data/x.png" width="1" height="1">
<div style="height:3000px"></div><button id="far" onclick="document.getElementById('out').textContent='far clicked'">Far away</button>
</body></html>`

func demoServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, demoPage)
	})
	mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Second</title></head><body><p>You are on the second page.</p><a href="/">Home</a></body></html>`)
	})
	mux.HandleFunc("/popup", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Popup</title></head><body><p>I am the popup.</p><button>Popup button</button></body></html>`)
	})
	mux.HandleFunc("/submitted", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<html><head><title>Submitted</title></head><body><p>user=%s country=%s agree=%s pw=%q</p></body></html>`,
			r.URL.Query().Get("user"), r.URL.Query().Get("country"), r.URL.Query().Get("agree"), r.URL.Query().Get("pw"))
	})
	mux.HandleFunc("/gone", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/covered", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Covered</title></head><body><button onclick="document.title='clicked'">Under the banner</button>
<div id="banner" style="position:fixed;left:0;top:0;width:100%;height:100%;background:#ccc">Cookie banner</div></body></html>`)
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Download</title></head><body><a href="/file.bin" download>Get the file</a></body></html>`)
	})
	mux.HandleFunc("/file.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="payload.bin"`)
		fmt.Fprint(w, "payload payload payload")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &hits
}

func newTestController(t *testing.T) *Controller {
	t.Helper()
	if os.Getenv("AGENTGO_SKIP_BROWSER_TESTS") != "" {
		t.Skip("browser tests disabled")
	}
	path, ok := FindBrowser("")
	if !ok {
		t.Skip("no Chrome or Edge installed")
	}
	dir, err := os.MkdirTemp("", "agentgo-browser-test-")
	if err != nil {
		t.Fatal(err)
	}
	c := New(Config{
		BrowserPath: path,
		ProfileDir:  dir + "/profile",
		ShotDir:     dir + "/shots",
		Headless:    true,
		Policy:      &Policy{AllowLoopback: true},
		ExtraArgs:   []string{"--no-sandbox"},
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = c.Close(ctx)
		// The browser lets go of its files a moment after it closes.
		for i := 0; i < 50; i++ {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
	return c
}

func opCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// refOf finds the ref of the first element whose line contains all of the parts.
func refOf(t *testing.T, snapshot string, parts ...string) int {
	t.Helper()
	for _, line := range strings.Split(snapshot, "\n") {
		if !strings.HasPrefix(line, "[") {
			continue
		}
		ok := true
		for _, p := range parts {
			if !strings.Contains(line, p) {
				ok = false
				break
			}
		}
		if ok {
			var n int
			if _, err := fmt.Sscanf(line, "[%d]", &n); err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Fatalf("no element with %v in:\n%s", parts, snapshot)
	return -1
}

func TestOpenReadsThePageLikeAPersonWould(t *testing.T) {
	c := newTestController(t)
	srv, _ := demoServer(t)
	out, err := c.Open(opCtx(t), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if out.Title != "Demo 页面" || !strings.HasPrefix(out.URL, srv.URL) || out.Status != 200 {
		t.Fatalf("%+v", out)
	}
	for _, want := range []string{
		`textbox "Username"`, `textbox(password) "Password"`, "[password field", `combobox "Country"`, "China | United States | Japan",
		`checkbox`, `[unchecked]`, `button "Greet"`, `link "Next page" -> ` + srv.URL + `/next`, "Order form",
	} {
		if !strings.Contains(out.Snapshot, want) {
			t.Errorf("snapshot lacks %q:\n%s", want, out.Snapshot)
		}
	}
}

func TestFillingAndClickingRunTheRealPage(t *testing.T) {
	c := newTestController(t)
	srv, _ := demoServer(t)
	ctx := opCtx(t)
	out, err := c.Open(ctx, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	user := refOf(t, out.Snapshot, `textbox "Username"`)
	out, err = c.Fill(ctx, user, "Username", "Alice", false)
	if err != nil || !strings.Contains(out.Message, `now holds "Alice"`) || !strings.Contains(out.Snapshot, `value="Alice"`) {
		t.Fatalf("%v %+v", err, out)
	}
	out, err = c.Click(ctx, refOf(t, out.Snapshot, `button "Greet"`), "Greet")
	if err != nil || !strings.Contains(out.Snapshot, "Hello Alice") {
		t.Fatalf("the page script did not run: %v\n%+v", err, out)
	}

	// A list is set by option text, a checkbox by clicking.
	out, err = c.Fill(ctx, refOf(t, out.Snapshot, `combobox "Country"`), "Country", "Japan", false)
	if err != nil || !strings.Contains(out.Snapshot, `value="Japan"`) {
		t.Fatalf("%v\n%+v", err, out)
	}
	out, err = c.Click(ctx, refOf(t, out.Snapshot, "checkbox"), "I agree")
	if err != nil || !strings.Contains(out.Snapshot, "[checked]") {
		t.Fatalf("%v\n%+v", err, out)
	}
	if _, err = c.Fill(ctx, refOf(t, out.Snapshot, "textbox(password)"), "Password", "hunter2", false); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("a password field was filled: %v", err)
	}
	if _, err = c.Fill(ctx, refOf(t, out.Snapshot, `combobox "Country"`), "Country", "Narnia", false); err == nil || !strings.Contains(err.Error(), "options") {
		t.Fatalf("%v", err)
	}

	// Enter in a field submits the form like a keyboard would.
	out, err = c.Fill(ctx, refOf(t, out.Snapshot, `textbox "Username"`), "Username", "Bob", true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Title != "Submitted" || !strings.Contains(out.Snapshot, `user=Bob country=jp agree=on pw=""`) {
		t.Fatalf("form was not submitted as filled:\n%+v", out)
	}
	back, err := c.Back(ctx)
	if err != nil || back.Title != "Demo 页面" {
		t.Fatalf("%v %+v", err, back)
	}
	// The last page in history cannot go back further than the first entry.
	if _, err := c.Back(ctx); err != nil {
		t.Fatalf("history has about:blank and the form page: %v", err)
	}
}

func TestAStaleOrMisnamedRefIsRefusedBeforeAnythingHappens(t *testing.T) {
	c := newTestController(t)
	srv, _ := demoServer(t)
	ctx := opCtx(t)
	out, err := c.Open(ctx, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	greet := refOf(t, out.Snapshot, `button "Greet"`)
	user := refOf(t, out.Snapshot, `textbox "Username"`)

	// The agent believes ref N is "Delete", the page says it is "Greet".
	if _, err := c.Click(ctx, greet, "Delete everything"); err == nil || !strings.Contains(err.Error(), "take a new snapshot") {
		t.Fatalf("a click with the wrong name went through: %v", err)
	}
	if out, _ := c.Snapshot(ctx, false); !strings.Contains(out.Snapshot, ">idle<") && !strings.Contains(out.Snapshot, "idle") {
		t.Fatalf("page changed: %s", out.Snapshot)
	}
	if _, err := c.Fill(ctx, user, "Search", "x", false); err == nil {
		t.Fatal("a fill with the wrong name went through")
	}
	if _, err := c.Click(ctx, 999, "Greet"); err == nil || !strings.Contains(err.Error(), "not in the latest snapshot") {
		t.Fatalf("%v", err)
	}
	// After the page moves on, refs from before mean nothing.
	if _, err := c.Open(ctx, srv.URL+"/next"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Click(ctx, greet, "Greet"); err == nil {
		t.Fatal("a ref from the previous page was accepted")
	}
}

func TestThePageCannotReachPrivateAddressesThroughTheBrowser(t *testing.T) {
	c := newTestController(t)
	srv, _ := demoServer(t)
	ctx := opCtx(t)
	for _, u := range []string{"http://10.0.0.1/", "http://169.254.169.254/latest/meta-data/", "file:///C:/Windows/win.ini", "javascript:alert(1)", "data:text/html,<p>x</p>"} {
		if _, err := c.Open(ctx, u); err == nil || !strings.Contains(err.Error(), "not opened") {
			t.Errorf("%s: %v", u, err)
		}
	}
	opened, err := c.Open(ctx, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	out := opened
	// The page tries to reach an intranet address by itself: through a script
	// and through an image. Neither may leave the machine.
	out, err = c.Click(ctx, refOf(t, out.Snapshot, `button "Probe"`), "Probe")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Snapshot, "reached") || !strings.Contains(out.Snapshot, "probe failed") {
		t.Fatalf("a request to a private address went out:\n%s", out.Snapshot)
	}
	joined := strings.Join(out.Notes, "\n")
	if !strings.Contains(joined, "10.255.255.1") {
		t.Fatalf("the refusal was not reported: %q", joined)
	}
	if opening := strings.Join(opened.Notes, "\n"); !strings.Contains(opening, "169.254.169.254") {
		t.Fatalf("the image request was not refused or reported: %q", opening)
	}
}

func TestDialogsPopupsAndStatusAreReported(t *testing.T) {
	c := newTestController(t)
	srv, _ := demoServer(t)
	ctx := opCtx(t)
	out, err := c.Open(ctx, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	out, err = c.Click(ctx, refOf(t, out.Snapshot, `button "Alert me"`), "Alert me")
	if err != nil || !strings.Contains(out.Snapshot, "alert closed") || !strings.Contains(strings.Join(out.Notes, "|"), "alert dialog appeared and was accepted: careful now") {
		t.Fatalf("%v %+v", err, out)
	}
	out, err = c.Click(ctx, refOf(t, out.Snapshot, `button "Ask me"`), "Ask me")
	if err != nil || !strings.Contains(out.Snapshot, "confirm=false") || !strings.Contains(strings.Join(out.Notes, "|"), "confirm dialog appeared and was dismissed") {
		t.Fatalf("a question was answered yes on the person's behalf: %v %+v", err, out)
	}

	out, err = c.Click(ctx, refOf(t, out.Snapshot, `link "Open popup"`), "Open popup")
	if err != nil {
		t.Fatal(err)
	}
	if out.Title != "Popup" || !strings.Contains(strings.Join(out.Notes, "|"), "new tab") || !strings.Contains(out.Snapshot, "Popup button") {
		t.Fatalf("the popup did not become the active page: %+v", out)
	}
	if out, err = c.Open(ctx, srv.URL+"/gone"); err != nil || out.Status != 404 {
		t.Fatalf("%v %+v", err, out)
	}
}

func TestScrollingKeysAndScreenshots(t *testing.T) {
	c := newTestController(t)
	srv, _ := demoServer(t)
	ctx := opCtx(t)
	out, err := c.Open(ctx, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Snapshot, "Scroll: 0 of ") {
		t.Fatalf("no scroll position:\n%s", out.Snapshot)
	}
	far := refOf(t, out.Snapshot, `button "Far away"`)
	if !strings.Contains(out.Snapshot, "[off screen]") {
		t.Fatalf("off-screen elements are not marked:\n%s", out.Snapshot)
	}
	out, err = c.Scroll(ctx, "bottom")
	if err != nil || strings.Contains(out.Snapshot, "Scroll: 0 of ") {
		t.Fatalf("%v\n%s", err, out.Snapshot)
	}
	out, err = c.Click(ctx, far, "Far away")
	if err != nil || !strings.Contains(out.Snapshot, "far clicked") {
		t.Fatalf("%v\n%s", err, out.Snapshot)
	}
	if out, err = c.Scroll(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Scroll(ctx, "sideways"); err == nil {
		t.Fatal("bad direction accepted")
	}
	if _, err = c.Scroll(ctx, "top"); err != nil {
		t.Fatal(err)
	}

	// Tab moves the focus to the next control; Control+A selects what is typed.
	out, _ = c.Snapshot(ctx, false)
	if _, err = c.Fill(ctx, refOf(t, out.Snapshot, `textbox "Username"`), "Username", "abc", false); err != nil {
		t.Fatal(err)
	}
	if out, err = c.Press(ctx, "Tab"); err != nil || !strings.Contains(out.Snapshot, `textbox(password) "Password"`) || !strings.Contains(lineWith(out.Snapshot, `"Password"`), "[focused]") {
		t.Fatalf("%v\n%s", err, out.Snapshot)
	}
	if _, err = c.Press(ctx, "Hyper+Z"); err == nil {
		t.Fatal("unknown modifier accepted")
	}

	path, shot, err := c.Screenshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 200 || string(data[1:4]) != "PNG" {
		t.Fatalf("not a PNG: %v %d bytes", err, len(data))
	}
	if shot.Title != "Demo 页面" {
		t.Fatalf("%+v", shot)
	}
}

func lineWith(s, part string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, part) {
			return l
		}
	}
	return ""
}

func TestTheBrowserCanBeClosedAndStartedAgain(t *testing.T) {
	c := newTestController(t)
	srv, _ := demoServer(t)
	ctx := opCtx(t)
	if _, err := c.Snapshot(ctx, false); err == nil {
		t.Fatal("a snapshot with no page open succeeded")
	}
	if _, err := c.Open(ctx, srv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Snapshot(ctx, false); err == nil {
		t.Fatal("the browser was still there after Close")
	}
	out, err := c.Open(ctx, srv.URL+"/next")
	if err != nil || out.Title != "Second" {
		t.Fatalf("%v %+v", err, out)
	}
}

func TestAnElementUnderAnOverlayIsNotClickedBlindly(t *testing.T) {
	c := newTestController(t)
	srv, _ := demoServer(t)
	ctx := opCtx(t)
	out, err := c.Open(ctx, srv.URL+"/covered")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Click(ctx, refOf(t, out.Snapshot, `button "Under the banner"`), "Under the banner")
	if err == nil || !strings.Contains(err.Error(), "covered by") || !strings.Contains(err.Error(), "Cookie banner") {
		t.Fatalf("the click went through a banner: %v", err)
	}
	if now, _ := c.Snapshot(ctx, false); now.Title != "Covered" {
		t.Fatalf("the button under the banner was pressed: %q", now.Title)
	}
}

func TestOpeningADownloadGivesTheTabBackToTheAgent(t *testing.T) {
	c := newTestController(t)
	srv, _ := demoServer(t)
	c.cfg.NavigateTimeout = 3 * time.Second
	ctx := opCtx(t)
	if _, err := c.Open(ctx, srv.URL+"/next"); err != nil {
		t.Fatal(err)
	}
	// Some PCs hold a download for a long time while they check the file; the
	// agent is told, and the page stays usable meanwhile.
	if _, err := c.Open(ctx, srv.URL+"/file.bin"); err == nil || !strings.Contains(err.Error(), "download") {
		t.Fatalf("opening a download was not reported: %v", err)
	}
	start := time.Now()
	out, err := c.Snapshot(ctx, false)
	if err != nil || time.Since(start) > 20*time.Second {
		t.Fatalf("the tab stayed stuck after the download: %v after %v", err, time.Since(start))
	}
	if out.Title != "Second" {
		t.Fatalf("%+v", out)
	}
}

func TestRefusedDownloadsAreReportedToTheAgent(t *testing.T) {
	c := New(Config{})
	tb := newTab("S", "T")
	c.active = tb
	c.onEvent(cdpEvent{Method: "Browser.downloadWillBegin", Params: []byte(`{"guid":"g1","suggestedFilename":"payload.bin"}`)})
	c.onEvent(cdpEvent{Method: "Browser.downloadProgress", Params: []byte(`{"guid":"g1","state":"inProgress"}`)})
	if n := tb.takeNotes(); len(n) != 0 {
		t.Fatalf("a download in progress was reported as blocked: %v", n)
	}
	c.onEvent(cdpEvent{Method: "Browser.downloadProgress", Params: []byte(`{"guid":"g1","state":"canceled"}`)})
	if n := tb.takeNotes(); len(n) != 1 || n[0] != "a file download was blocked: payload.bin" {
		t.Fatalf("notes: %v", n)
	}
}

// A real browser only reports a refused download after its own file check, which
// can take 45 seconds on a PC without internet; run with AGENTGO_BROWSER_SLOW_TESTS=1.
func TestPagesCannotSaveFilesToThePC(t *testing.T) {
	if os.Getenv("AGENTGO_BROWSER_SLOW_TESTS") == "" {
		t.Skip("slow; set AGENTGO_BROWSER_SLOW_TESTS=1")
	}
	c := newTestController(t)
	srv, _ := demoServer(t)
	// Point the browser's download folder at a folder we can inspect.
	downloads := t.TempDir()
	prefs := fmt.Sprintf(`{"download":{"default_directory":%q,"prompt_for_download":false}}`, downloads)
	if err := os.MkdirAll(c.cfg.ProfileDir+"/Default", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.cfg.ProfileDir+"/Default/Preferences", []byte(prefs), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := c.Open(ctx, srv.URL+"/download")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Click(ctx, refOf(t, out.Snapshot, `link "Get the file"`), "Get the file"); err != nil {
		t.Fatal(err)
	}
	blocked := false
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline) && !blocked; time.Sleep(500 * time.Millisecond) {
		c.mu.Lock()
		tb := c.active
		c.mu.Unlock()
		tb.mu.Lock()
		blocked = strings.Contains(strings.Join(tb.notes, "|"), "a file download was blocked: payload.bin")
		tb.mu.Unlock()
	}
	if !blocked {
		t.Fatal("the refused download was not reported")
	}
	if entries, _ := os.ReadDir(downloads); len(entries) != 0 {
		t.Fatalf("the page saved %d file(s) to the PC", len(entries))
	}
}
