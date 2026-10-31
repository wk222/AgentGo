package webshell

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type fakeService struct{}

func (fakeService) Echo(s string) string     { return "echo:" + s }
func (fakeService) Sum(a, b float64) float64 { return a + b }
func (fakeService) Fail() (string, error)    { return "", errors.New("boom") }
func (fakeService) Boom()                    { panic("kaput") }
func (fakeService) WithCtx(ctx context.Context, n int) (int, error) {
	if ctx == nil {
		return 0, errors.New("no ctx")
	}
	return n * 2, nil
}
func (fakeService) Many(prefix string, rest ...string) string {
	return prefix + strings.Join(rest, ",")
}
func (fakeService) ServiceStartup() error  { return nil }
func (fakeService) Window() map[string]any { return map[string]any{"open_url": "/?x=1"} }

func newTestServer() *httptest.Server {
	assets := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	s := New(Options{Service: fakeService{}, Assets: assets, Token: "tok-123456789012345678901234567890"})
	return httptest.NewServer(s.Handler())
}

func post(t *testing.T, c *http.Client, url string, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func authedClient(t *testing.T, base string) *http.Client {
	t.Helper()
	jar := &simpleJar{}
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(base + "/?t=tok-123456789012345678901234567890&view=a")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/?view=a" {
		t.Fatalf("token visit: status %d location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if len(jar.cookies) != 1 || !jar.cookies[0].HttpOnly || jar.cookies[0].Name != CookieName {
		t.Fatalf("cookie not set: %+v", jar.cookies)
	}
	return c
}

type simpleJar struct{ cookies []*http.Cookie }

func (j *simpleJar) SetCookies(_ *url.URL, cs []*http.Cookie) { j.cookies = append(j.cookies, cs...) }
func (j *simpleJar) Cookies(*url.URL) []*http.Cookie          { return j.cookies }

func TestCallRequiresTokenCookie(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	status, _ := post(t, ts.Client(), ts.URL+"/api/ipc/call", `{"method":"Echo","args":["x"]}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("no cookie: status %d", status)
	}
	c := authedClient(t, ts.URL)
	status, body := post(t, c, ts.URL+"/api/ipc/call", `{"method":"Echo","args":["hi"]}`)
	if status != 200 || body["result"] != "echo:hi" {
		t.Fatalf("call: %d %v", status, body)
	}
}

func TestCallDispatch(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	c := authedClient(t, ts.URL)
	cases := []struct {
		body   string
		status int
		want   any
	}{
		{`{"method":"Sum","args":[2,3.5]}`, 200, 5.5},
		{`{"method":"WithCtx","args":[21]}`, 200, 42.0},
		{`{"method":"Many","args":["a","b","c"]}`, 200, "ab,c"},
		{`{"method":"Many","args":["a"]}`, 200, "a"},
		{`{"method":"Fail","args":[]}`, 500, nil},
		{`{"method":"Boom","args":[]}`, 500, nil},
		{`{"method":"Nope","args":[]}`, 404, nil},
		{`{"method":"ServiceStartup","args":[]}`, 404, nil},
		{`{"method":"Echo","args":[1]}`, 500, nil},
		{`{"method":"Echo","args":["a","b"]}`, 500, nil},
	}
	for _, tc := range cases {
		status, body := post(t, c, ts.URL+"/api/ipc/call", tc.body)
		if status != tc.status {
			t.Errorf("%s: status %d want %d (%v)", tc.body, status, tc.status, body)
			continue
		}
		if tc.status == 200 && body["result"] != tc.want {
			t.Errorf("%s: result %v want %v", tc.body, body["result"], tc.want)
		}
	}
}

func TestRejectsForeignHostAndOrigin(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	c := authedClient(t, ts.URL)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/ipc/ping", nil)
	req.Host = "evil.example"
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign host: %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/ipc/call", strings.NewReader(`{"method":"Echo","args":["x"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", resp.StatusCode)
	}

	// Form posts are not JSON: they must not reach the dispatcher.
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/ipc/call", strings.NewReader("method=Echo"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form post: %d", resp.StatusCode)
	}
}

func TestStaticAndSPAFallback(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	get := func(path, ua string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var sb strings.Builder
		_, _ = io.Copy(&sb, resp.Body)
		return resp.StatusCode, sb.String()
	}
	if s, b := get("/", ""); s != 200 || !strings.Contains(b, "app") {
		t.Fatalf("index: %d %q", s, b)
	}
	if s, b := get("/some/route", ""); s != 200 || !strings.Contains(b, "app") {
		t.Fatalf("spa fallback: %d %q", s, b)
	}
	if s, _ := get("/assets/missing.js", ""); s != 404 {
		t.Fatalf("missing asset: %d", s)
	}
	if s, b := get("/assets/app.js", ""); s != 200 || !strings.Contains(b, "console.log") {
		t.Fatalf("asset: %d %q", s, b)
	}
	if s, b := get("/wails/runtime.js", ""); s != 200 || !strings.Contains(b, "ByName") {
		t.Fatalf("runtime.js: %d", s)
	}
	if _, b := get("/", "Mozilla/5.0 (Windows NT 6.1; Trident/7.0; rv:11.0) like Gecko"); !strings.Contains(b, "Chrome") {
		t.Fatalf("IE should get the upgrade page, got %q", b)
	}
}

func TestEventStream(t *testing.T) {
	assets := fstest.MapFS{"index.html": {Data: []byte("x")}}
	s := New(Options{Service: fakeService{}, Assets: assets})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ipc/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	deadline := time.Now().Add(2 * time.Second)
	for s.hub.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	payload := map[string]any{"k": "v"}
	s.Publish("chat:chunk", payload)
	payload["k"] = "changed" // must not leak into the already queued event

	lines := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") {
				lines <- strings.TrimPrefix(sc.Text(), "data: ")
				return
			}
		}
	}()
	select {
	case line := <-lines:
		var ev struct {
			Name string         `json:"name"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Name != "chat:chunk" || ev.Data["k"] != "v" {
			t.Fatalf("event %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event received")
	}
}

func TestLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1:8788": true, "localhost:1": true, "[::1]:80": true, "127.0.0.1": true,
		"evil.example": false, "192.168.1.5:8788": false, "": false, "localhost.evil.example": false,
	} {
		if got := loopbackHost(host); got != want {
			t.Errorf("%q: got %v want %v", host, got, want)
		}
	}
}

func TestTokenIsStable(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreateToken(dir)
	if err != nil || len(a) < 32 {
		t.Fatalf("token %q err %v", a, err)
	}
	b, _ := LoadOrCreateToken(dir)
	if a != b {
		t.Fatal("token must persist across runs so the browser cookie stays valid")
	}
}
