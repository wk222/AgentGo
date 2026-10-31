package browsercontrol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config says how the browser is found and what it may reach.
type Config struct {
	BrowserPath     string        // empty: look in the usual places
	ProfileDir      string        // AgentGo's own browser profile
	ShotDir         string        // where screenshots are written
	Headless        bool          // no visible window
	Policy          *Policy       // nil: public addresses only
	StartTimeout    time.Duration // how long to wait for a new browser
	NavigateTimeout time.Duration // how long Page.navigate may take; default 20s
	ExtraArgs       []string      // appended to the browser command line (tests)
}

// Controller drives one browser. Operations run one at a time: the agent may
// issue several tool calls at once, but a page has a single state.
type Controller struct {
	cfg Config
	sem chan struct{}

	mu        sync.Mutex
	conn      *cdpConn
	off       func()
	cmd       *exec.Cmd
	exited    chan struct{}
	tabs      map[string]*tab // by session
	byTarget  map[string]*tab
	active    *tab
	downloads map[string]string // download guid -> file name, until it is cancelled
}

// Outcome is what an operation reports back to the agent.
type Outcome struct {
	URL      string
	Title    string
	Status   int // HTTP status of the page document, 0 when unknown
	Message  string
	Notes    []string
	Snapshot string
}

func New(cfg Config) *Controller {
	if cfg.Policy == nil {
		cfg.Policy = &Policy{}
	}
	return &Controller{cfg: cfg, sem: make(chan struct{}, 1), tabs: map[string]*tab{}, byTarget: map[string]*tab{}, downloads: map[string]string{}}
}

const (
	opTimeout       = 90 * time.Second
	navigateTimeout = 20 * time.Second
	evalTimeout     = 6 * time.Second
	quietFor        = 350 * time.Millisecond
	backgroundAfter = 3 * time.Second // a request older than this is not "the page still loading"
	maxNotes        = 8
)

type tab struct {
	session, target string
	ready           chan struct{}

	mu       sync.Mutex
	inflight map[string]time.Time
	activity time.Time
	status   int
	notes    []string
	gone     bool
}

func newTab(session, target string) *tab {
	return &tab{session: session, target: target, ready: make(chan struct{}), inflight: map[string]time.Time{}, activity: time.Now()}
}

func (t *tab) touch() {
	t.mu.Lock()
	t.activity = time.Now()
	t.mu.Unlock()
}

func (t *tab) note(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, n := range t.notes {
		if n == s {
			return
		}
	}
	if len(t.notes) < maxNotes {
		t.notes = append(t.notes, s)
	}
}

func (t *tab) takeNotes() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.notes
	t.notes = nil
	return n
}

func (t *tab) quiet(d time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Since(t.activity) < d {
		return false
	}
	for _, at := range t.inflight {
		if time.Since(at) < backgroundAfter {
			return false
		}
	}
	return true
}

// ---- operation plumbing ----

func (c *Controller) acquire(ctx context.Context) (func(), error) {
	select {
	case c.sem <- struct{}{}:
		return func() { <-c.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// run takes the operation lock, makes sure a browser is running and hands fn
// the page to work on. create allows opening a first page when none exists.
func (c *Controller) run(ctx context.Context, create bool, fn func(ctx context.Context, t *tab) (*Outcome, error)) (*Outcome, error) {
	release, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	if !create && !c.running() {
		// Looking at or acting on a page never starts a browser by itself.
		return nil, errors.New("no page is open; use browser_open first")
	}
	var t *tab
	for attempt := 0; ; attempt++ {
		err = c.ensure(ctx)
		if err == nil {
			t, err = c.activeTab(ctx, create)
		}
		if err == nil {
			break
		}
		// A browser that was closing when we met it: start afresh once.
		if errors.Is(err, errConnClosed) && attempt == 0 {
			c.reset()
			continue
		}
		return nil, err
	}
	out, err := fn(ctx, t)
	if err != nil && errors.Is(err, errConnClosed) {
		c.reset()
		return nil, errors.New("the browser window was closed; open a page again to start a new one")
	}
	return out, err
}

// running reports whether a live browser connection exists.
func (c *Controller) running() bool {
	conn := c.connOrNil()
	if conn == nil {
		return false
	}
	select {
	case <-conn.Done():
		return false
	default:
		return true
	}
}

func (c *Controller) reset() {
	c.mu.Lock()
	conn, off := c.conn, c.off
	c.conn, c.off, c.cmd, c.exited = nil, nil, nil, nil
	c.tabs = map[string]*tab{}
	c.byTarget = map[string]*tab{}
	c.active = nil
	c.mu.Unlock()
	if off != nil {
		off()
	}
	if conn != nil {
		conn.close()
	}
}

func (c *Controller) ensure(ctx context.Context) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		select {
		case <-conn.Done():
		default:
			return nil
		}
		c.reset()
	}
	conn, cmd, exited, err := connectBrowser(ctx, c.cfg)
	if err != nil {
		return err
	}
	// The connection is recorded before events are delivered: handlers look it up.
	c.mu.Lock()
	c.conn, c.cmd, c.exited = conn, cmd, exited
	c.mu.Unlock()
	off := conn.on(c.onEvent)
	c.mu.Lock()
	c.off = off
	c.mu.Unlock()

	// Pages must never save files to the PC on their own.
	if err := conn.call(ctx, "", "Browser.setDownloadBehavior", map[string]any{"behavior": "deny", "eventsEnabled": true}, nil); err != nil {
		log.Printf("[browsercontrol] could not block downloads: %v", err)
	}
	// Every page, including popups, is held at its first instruction until the
	// address policy is installed on it.
	if err := conn.call(ctx, "", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}, nil); err != nil {
		c.reset()
		return fmt.Errorf("control the browser: %w", err)
	}
	var list struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := conn.call(ctx, "", "Target.getTargets", nil, &list); err == nil {
		time.Sleep(150 * time.Millisecond) // let automatic attachments of existing pages arrive first
		for _, ti := range list.TargetInfos {
			if ti.Type != "page" {
				continue
			}
			c.mu.Lock()
			_, known := c.byTarget[ti.TargetID]
			c.mu.Unlock()
			if !known {
				_ = conn.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": ti.TargetID, "flatten": true}, nil)
			}
		}
	}
	return nil
}

func (c *Controller) activeTab(ctx context.Context, create bool) (*tab, error) {
	c.mu.Lock()
	t, conn := c.active, c.conn
	c.mu.Unlock()
	if t == nil {
		// Pages found at start-up are still being prepared.
		deadline := time.Now().Add(1500 * time.Millisecond)
		for t == nil && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			c.mu.Lock()
			t = c.active
			c.mu.Unlock()
		}
	}
	if t == nil {
		if !create {
			return nil, errors.New("no page is open; use browser_open first")
		}
		var res struct {
			TargetID string `json:"targetId"`
		}
		if err := conn.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &res); err != nil {
			return nil, err
		}
		var err error
		if t, err = c.awaitTarget(ctx, res.TargetID); err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.active = t
		c.mu.Unlock()
	}
	select {
	case <-t.ready:
	case <-time.After(10 * time.Second):
		return nil, errors.New("the page did not become ready")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return t, nil
}

func (c *Controller) awaitTarget(ctx context.Context, target string) (*tab, error) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		t := c.byTarget[target]
		c.mu.Unlock()
		if t != nil {
			return t, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(30 * time.Millisecond):
		}
	}
	return nil, errors.New("the new page did not appear")
}

// ---- events ----

type targetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	OpenerID string `json:"openerId"`
}

func (c *Controller) onEvent(ev cdpEvent) {
	switch ev.Method {
	case "Target.attachedToTarget":
		var p struct {
			SessionID          string     `json:"sessionId"`
			TargetInfo         targetInfo `json:"targetInfo"`
			WaitingForDebugger bool       `json:"waitingForDebugger"`
		}
		if json.Unmarshal(ev.Params, &p) == nil {
			go c.attached(p.SessionID, p.TargetInfo, p.WaitingForDebugger)
		}
	case "Target.detachedFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(ev.Params, &p) == nil {
			c.dropSession(p.SessionID)
		}
	case "Fetch.requestPaused":
		go c.paused(ev.Session, ev.Params)
	case "Network.requestWillBeSent":
		var p struct {
			RequestID string `json:"requestId"`
			Type      string `json:"type"`
		}
		if t := c.tabFor(ev.Session); t != nil && json.Unmarshal(ev.Params, &p) == nil {
			t.mu.Lock()
			t.activity = time.Now()
			switch p.Type {
			case "EventSource", "WebSocket", "Ping":
			default:
				t.inflight[p.RequestID] = time.Now()
			}
			t.mu.Unlock()
		}
	case "Network.loadingFinished", "Network.loadingFailed":
		var p struct {
			RequestID string `json:"requestId"`
		}
		if t := c.tabFor(ev.Session); t != nil && json.Unmarshal(ev.Params, &p) == nil {
			t.mu.Lock()
			t.activity = time.Now()
			delete(t.inflight, p.RequestID)
			t.mu.Unlock()
		}
	case "Network.responseReceived":
		var p struct {
			Type     string `json:"type"`
			Response struct {
				Status int `json:"status"`
			} `json:"response"`
		}
		if t := c.tabFor(ev.Session); t != nil && json.Unmarshal(ev.Params, &p) == nil && p.Type == "Document" {
			t.mu.Lock()
			t.status = p.Response.Status
			t.mu.Unlock()
		}
	case "Page.frameNavigated", "Page.navigatedWithinDocument", "Page.frameStartedLoading":
		if t := c.tabFor(ev.Session); t != nil {
			t.touch()
		}
	case "Browser.downloadWillBegin":
		var p struct {
			GUID     string `json:"guid"`
			Filename string `json:"suggestedFilename"`
		}
		if json.Unmarshal(ev.Params, &p) == nil {
			c.mu.Lock()
			c.downloads[p.GUID] = p.Filename
			c.mu.Unlock()
		}
	case "Browser.downloadProgress":
		var p struct {
			GUID  string `json:"guid"`
			State string `json:"state"`
		}
		if json.Unmarshal(ev.Params, &p) == nil && p.State == "canceled" {
			c.mu.Lock()
			name := c.downloads[p.GUID]
			delete(c.downloads, p.GUID)
			t := c.active
			c.mu.Unlock()
			if t != nil {
				t.note("a file download was blocked: " + name)
			}
		}
	case "Page.javascriptDialogOpening":
		go c.dialog(ev.Session, ev.Params)
	}
}

func (c *Controller) tabFor(session string) *tab {
	if session == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tabs[session]
}

func (c *Controller) connOrNil() *cdpConn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

func (c *Controller) attached(session string, info targetInfo, waiting bool) {
	conn := c.connOrNil()
	if conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resume := func() {
		if waiting {
			_ = conn.call(ctx, session, "Runtime.runIfWaitingForDebugger", nil, nil)
		}
	}
	fetch := map[string]any{"patterns": []map[string]any{{"urlPattern": "*"}}}
	switch info.Type {
	case "page":
		c.mu.Lock()
		if _, dup := c.byTarget[info.TargetID]; dup {
			c.mu.Unlock()
			_ = conn.call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": session}, nil)
			return
		}
		t := newTab(session, info.TargetID)
		c.tabs[session] = t
		c.byTarget[info.TargetID] = t
		c.mu.Unlock()
		// A page that starts paused answers only the commands that do not need its
		// renderer until it is resumed. The address policy and frame attachment
		// go first; the page is resumed; Page and Network follow.
		type step struct {
			method string
			params any
		}
		var failed error
		run := func(steps ...step) {
			for _, step := range steps {
				if failed != nil {
					return
				}
				if err := conn.call(ctx, session, step.method, step.params, nil); err != nil {
					failed = fmt.Errorf("%s: %w", step.method, err)
				}
			}
		}
		run(step{"Fetch.enable", fetch}, step{"Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}})
		if failed == nil {
			resume()
			run(step{"Page.enable", nil}, step{"Network.enable", nil})
		}
		if failed != nil {
			// A page without the address policy must not be used.
			log.Printf("[browsercontrol] page left unusable: %v", failed)
			t.mu.Lock()
			t.gone = true
			t.mu.Unlock()
			_ = conn.call(ctx, "", "Target.closeTarget", map[string]any{"targetId": info.TargetID}, nil)
			c.dropSession(session)
			return
		}
		close(t.ready)
		c.mu.Lock()
		switch {
		case c.active == nil:
			c.active = t
		case info.OpenerID != "" && c.active.target == info.OpenerID:
			c.active.note("a new tab opened and is now the active page")
			c.active = t
		}
		c.mu.Unlock()
	case "iframe":
		// A frame in its own process makes its requests on its own session.
		if err := conn.call(ctx, session, "Fetch.enable", fetch, nil); err != nil {
			log.Printf("[browsercontrol] frame left unguarded, detaching: %v", err)
			_ = conn.call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": session}, nil)
			return
		}
		_ = conn.call(ctx, session, "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}, nil)
		resume()
	default:
		resume()
		_ = conn.call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": session}, nil)
	}
}

func (c *Controller) dropSession(session string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.tabs[session]
	if t == nil {
		return
	}
	delete(c.tabs, session)
	delete(c.byTarget, t.target)
	t.mu.Lock()
	t.gone = true
	t.mu.Unlock()
	if c.active == t {
		c.active = nil
		var rest []*tab
		for _, o := range c.tabs {
			rest = append(rest, o)
		}
		sort.Slice(rest, func(i, j int) bool { return rest[i].target < rest[j].target })
		if len(rest) > 0 {
			c.active = rest[len(rest)-1]
		}
	}
}

// paused answers one intercepted request: let it through or refuse it.
func (c *Controller) paused(session string, params json.RawMessage) {
	var p struct {
		RequestID string `json:"requestId"`
		Request   struct {
			URL string `json:"url"`
		} `json:"request"`
		ResourceType string `json:"resourceType"`
	}
	conn := c.connOrNil()
	if conn == nil || json.Unmarshal(params, &p) != nil || p.RequestID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := c.cfg.Policy.CheckRequest(ctx, p.Request.URL, p.ResourceType == "Document"); err != nil {
		_ = conn.call(ctx, session, "Fetch.failRequest", map[string]any{"requestId": p.RequestID, "errorReason": "BlockedByClient"}, nil)
		c.noteBlocked(session, p.Request.URL, err)
		return
	}
	_ = conn.call(ctx, session, "Fetch.continueRequest", map[string]any{"requestId": p.RequestID}, nil)
}

func (c *Controller) noteBlocked(session, raw string, why error) {
	t := c.tabFor(session)
	if t == nil {
		c.mu.Lock()
		t = c.active
		c.mu.Unlock()
	}
	if t == nil {
		return
	}
	host := raw
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		host = u.Scheme + "://" + u.Host
	}
	t.note(fmt.Sprintf("blocked a request to %s: %v", host, why))
}

func (c *Controller) dialog(session string, params json.RawMessage) {
	var p struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	conn := c.connOrNil()
	if conn == nil || json.Unmarshal(params, &p) != nil {
		return
	}
	// Alerts and "leave this page?" prompts are accepted so the page can go on;
	// questions are declined, because answering yes is a decision for the person.
	accept := p.Type == "alert" || p.Type == "beforeunload"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = conn.call(ctx, session, "Page.handleJavaScriptDialog", map[string]any{"accept": accept}, nil)
	verb := "dismissed"
	if accept {
		verb = "accepted"
	}
	if t := c.tabFor(session); t != nil {
		msg := []rune(oneLine(p.Message))
		if len(msg) > 200 {
			msg = append(msg[:200], '…')
		}
		t.note(fmt.Sprintf("a %s dialog appeared and was %s: %s", p.Type, verb, string(msg)))
		t.touch()
	}
}

// ---- page access ----

func (c *Controller) evalString(ctx context.Context, t *tab, expr string) (string, error) {
	conn := c.connOrNil()
	if conn == nil {
		return "", errConnClosed
	}
	var res struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	// A page whose main thread is stuck (a script loop, a navigation that is
	// waiting on a download check) must not hold the agent for the whole operation.
	ectx, cancel := context.WithTimeout(ctx, evalTimeout)
	defer cancel()
	err := conn.call(ectx, t.session, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}, &res)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return "", errPageBusy
		}
		return "", err
	}
	if res.Exception != nil {
		d := res.Exception.Exception.Description
		if d == "" {
			d = res.Exception.Text
		}
		return "", fmt.Errorf("page script failed: %s", firstLine(d))
	}
	var s string
	if len(res.Result.Value) > 0 && json.Unmarshal(res.Result.Value, &s) != nil {
		s = string(res.Result.Value)
	}
	return s, nil
}

var errPageBusy = errors.New("the page is not responding (it may be busy loading or waiting on a file download); try again in a moment")

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// settle waits until the page has stopped changing: no fresh requests, no
// recent navigation and the document loaded. It gives up at maxWait; a page
// that keeps polling is still returned, as it is.
func (c *Controller) settle(ctx context.Context, t *tab, maxWait time.Duration) {
	t.touch()
	deadline := time.Now().Add(maxWait)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		sleepCtx(ctx, 60*time.Millisecond)
		if t.quiet(quietFor) {
			rs, err := c.evalString(ctx, t, "document.readyState")
			if err == nil && (rs == "complete" || (rs == "interactive" && t.quiet(1500*time.Millisecond))) {
				return
			}
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// observe describes the page as it is now.
func (c *Controller) observe(ctx context.Context, t *tab, textLimit int, message string) (*Outcome, error) {
	// An action may have opened another tab; report the one in front.
	c.mu.Lock()
	cur := c.active
	c.mu.Unlock()
	var earlier []string
	if cur != nil && cur != t {
		select {
		case <-cur.ready:
			earlier = t.takeNotes()
			t = cur
			c.settle(ctx, t, 8*time.Second)
		case <-ctx.Done():
		}
	}
	var snap snapshot
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		var raw string
		raw, err = c.evalString(ctx, t, snapshotScript(120, textLimit))
		if err == nil {
			snap, err = parseSnapshot(raw)
			if err == nil {
				break
			}
		}
		if errors.Is(err, errConnClosed) || ctx.Err() != nil {
			return nil, err
		}
		sleepCtx(ctx, 250*time.Millisecond) // the page was still navigating
	}
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	status := t.status
	t.mu.Unlock()
	return &Outcome{URL: snap.URL, Title: snap.Title, Status: status, Message: message, Notes: append(earlier, t.takeNotes()...), Snapshot: snap.render()}, nil
}

// ---- operations ----

const (
	defaultText = 2500
	fullText    = 8000
)

// Open navigates the active page, or a new one, to rawURL.
func (c *Controller) Open(ctx context.Context, rawURL string) (*Outcome, error) {
	if err := c.cfg.Policy.CheckNavigation(ctx, rawURL); err != nil {
		return nil, fmt.Errorf("not opened: %w", err)
	}
	return c.run(ctx, true, func(ctx context.Context, t *tab) (*Outcome, error) {
		conn := c.connOrNil()
		t.mu.Lock()
		t.status = 0
		t.mu.Unlock()
		t.touch()
		var res struct {
			ErrorText string `json:"errorText"`
		}
		// A link to a file download keeps this command waiting for a long time in
		// some browsers, so it is given a limited time of its own.
		limit := c.cfg.NavigateTimeout
		if limit <= 0 {
			limit = navigateTimeout
		}
		navCtx, cancelNav := context.WithTimeout(ctx, limit)
		err := conn.call(navCtx, t.session, "Page.navigate", map[string]any{"url": rawURL}, &res)
		cancelNav()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				// Give the tab back: cancel the load that never came to an end.
				sctx, stop := context.WithTimeout(ctx, 3*time.Second)
				_ = conn.call(sctx, t.session, "Page.stopLoading", nil, nil)
				stop()
				return nil, errors.New("the address did not answer in time; if it is a file download, downloads are blocked and nothing was saved")
			}
			return nil, err
		}
		if res.ErrorText != "" {
			notes := t.takeNotes()
			msg := "the page could not be loaded: " + strings.TrimPrefix(res.ErrorText, "net::")
			if res.ErrorText == "net::ERR_ABORTED" {
				msg = "the address is a file download (or the load was cancelled); downloads are blocked, so nothing was saved"
			}
			if len(notes) > 0 {
				msg += " (" + strings.Join(notes, "; ") + ")"
			}
			return nil, errors.New(msg)
		}
		c.settle(ctx, t, 25*time.Second)
		return c.observe(ctx, t, defaultText, "")
	})
}

// Snapshot describes the page without changing it.
func (c *Controller) Snapshot(ctx context.Context, full bool) (*Outcome, error) {
	return c.run(ctx, false, func(ctx context.Context, t *tab) (*Outcome, error) {
		limit := defaultText
		if full {
			limit = fullText
		}
		return c.observe(ctx, t, limit, "")
	})
}

type refInfo struct {
	Error     string  `json:"error"`
	OK        bool    `json:"ok"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	CoveredBy string  `json:"coveredBy"`
	Kind      string  `json:"kind"`
	Value     string  `json:"value"`
	Secret    bool    `json:"secret"`
}

func (c *Controller) evalRef(ctx context.Context, t *tab, script string) (refInfo, error) {
	raw, err := c.evalString(ctx, t, script)
	if err != nil {
		return refInfo{}, err
	}
	var ri refInfo
	if err := json.Unmarshal([]byte(raw), &ri); err != nil {
		return refInfo{}, fmt.Errorf("the page returned an unreadable answer: %w", err)
	}
	return ri, nil
}

// confirmTarget checks that the element behind ref is still the one named.
func (c *Controller) confirmTarget(ctx context.Context, t *tab, ref int, name string) (refInfo, error) {
	ri, err := c.evalRef(ctx, t, refInfoScript(ref))
	if err != nil {
		return ri, err
	}
	if ri.Error != "" {
		return ri, errors.New(ri.Error)
	}
	if !sameName(name, ri.Name) {
		return ri, fmt.Errorf("[%d] is now %s %q, not %q: the page changed since the snapshot; take a new snapshot and choose again", ref, ri.Role, ri.Name, name)
	}
	return ri, nil
}

// Click presses the element listed as ref, after checking its name still matches.
func (c *Controller) Click(ctx context.Context, ref int, name string) (*Outcome, error) {
	return c.run(ctx, false, func(ctx context.Context, t *tab) (*Outcome, error) {
		if _, err := c.confirmTarget(ctx, t, ref, name); err != nil {
			return nil, err
		}
		ri, err := c.evalRef(ctx, t, refTargetScript(ref))
		if err != nil {
			return nil, err
		}
		if ri.Error != "" {
			return nil, errors.New(ri.Error)
		}
		if !ri.OK {
			return nil, fmt.Errorf("[%d] %q is covered by %s; close or scroll away what covers it first", ref, ri.Name, ri.CoveredBy)
		}
		conn := c.connOrNil()
		t.touch()
		for _, ev := range []map[string]any{
			{"type": "mouseMoved", "x": ri.X, "y": ri.Y},
			{"type": "mousePressed", "x": ri.X, "y": ri.Y, "button": "left", "buttons": 1, "clickCount": 1},
			{"type": "mouseReleased", "x": ri.X, "y": ri.Y, "button": "left", "buttons": 0, "clickCount": 1},
		} {
			if err := conn.call(ctx, t.session, "Input.dispatchMouseEvent", ev, nil); err != nil {
				return nil, err
			}
		}
		c.settle(ctx, t, 10*time.Second)
		return c.observe(ctx, t, defaultText, fmt.Sprintf("clicked [%d] %s %q", ref, ri.Role, ri.Name))
	})
}

// Fill puts text in a field (or picks an option in a list), optionally pressing Enter.
func (c *Controller) Fill(ctx context.Context, ref int, name, text string, submit bool) (*Outcome, error) {
	return c.run(ctx, false, func(ctx context.Context, t *tab) (*Outcome, error) {
		if _, err := c.confirmTarget(ctx, t, ref, name); err != nil {
			return nil, err
		}
		ri, err := c.evalRef(ctx, t, refFocusScript(ref, text))
		if err != nil {
			return nil, err
		}
		if ri.Error != "" {
			return nil, errors.New(ri.Error)
		}
		conn := c.connOrNil()
		t.touch()
		msg := ""
		if ri.Kind == "select" {
			msg = fmt.Sprintf("chose %q in [%d] %q", ri.Value, ref, ri.Name)
		} else {
			if text == "" {
				if err := c.sendKey(ctx, t, "Backspace"); err != nil {
					return nil, err
				}
			} else if err := conn.call(ctx, t.session, "Input.insertText", map[string]any{"text": text}, nil); err != nil {
				return nil, err
			}
			now, _ := c.evalString(ctx, t, refValueScript(ref))
			msg = fmt.Sprintf("typed into [%d] %q; the field now holds %q", ref, ri.Name, now)
		}
		if submit && ri.Kind != "select" {
			if err := c.sendKey(ctx, t, "Enter"); err != nil {
				return nil, err
			}
			msg += "; pressed Enter"
		}
		c.settle(ctx, t, 10*time.Second)
		return c.observe(ctx, t, defaultText, msg)
	})
}

// Press sends a key or a chord such as "Enter" or "Control+A" to the page.
func (c *Controller) Press(ctx context.Context, key string) (*Outcome, error) {
	if _, err := parseKey(key); err != nil {
		return nil, err
	}
	return c.run(ctx, false, func(ctx context.Context, t *tab) (*Outcome, error) {
		t.touch()
		if err := c.sendKey(ctx, t, key); err != nil {
			return nil, err
		}
		c.settle(ctx, t, 10*time.Second)
		return c.observe(ctx, t, defaultText, "pressed "+key)
	})
}

// Scroll moves the page: "down" and "up" by most of a window, "top" and "bottom" to the ends.
func (c *Controller) Scroll(ctx context.Context, direction string) (*Outcome, error) {
	direction = strings.ToLower(strings.TrimSpace(direction))
	switch direction {
	case "down", "up", "top", "bottom":
	default:
		return nil, errors.New(`direction must be "down", "up", "top" or "bottom"`)
	}
	return c.run(ctx, false, func(ctx context.Context, t *tab) (*Outcome, error) {
		conn := c.connOrNil()
		t.touch()
		switch direction {
		case "top", "bottom":
			if _, err := c.evalString(ctx, t, scrollToScript(direction == "bottom")); err != nil {
				return nil, err
			}
		default:
			raw, err := c.evalString(ctx, t, viewportScript)
			if err != nil {
				return nil, err
			}
			var vp struct{ W, H float64 }
			_ = json.Unmarshal([]byte(raw), &vp)
			if vp.H <= 0 {
				vp.W, vp.H = 1000, 700
			}
			delta := vp.H * 0.8
			if direction == "up" {
				delta = -delta
			}
			if err := conn.call(ctx, t.session, "Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": vp.W / 2, "y": vp.H / 2, "deltaX": 0, "deltaY": delta}, nil); err != nil {
				return nil, err
			}
		}
		sleepCtx(ctx, 250*time.Millisecond)
		c.settle(ctx, t, 8*time.Second)
		return c.observe(ctx, t, defaultText, "scrolled "+direction)
	})
}

// Back returns to the previous page of the active tab.
func (c *Controller) Back(ctx context.Context) (*Outcome, error) {
	return c.run(ctx, false, func(ctx context.Context, t *tab) (*Outcome, error) {
		conn := c.connOrNil()
		var h struct {
			CurrentIndex int `json:"currentIndex"`
			Entries      []struct {
				ID int `json:"id"`
			} `json:"entries"`
		}
		if err := conn.call(ctx, t.session, "Page.getNavigationHistory", nil, &h); err != nil {
			return nil, err
		}
		if h.CurrentIndex <= 0 || h.CurrentIndex >= len(h.Entries) {
			return nil, errors.New("there is no earlier page to go back to")
		}
		t.touch()
		if err := conn.call(ctx, t.session, "Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[h.CurrentIndex-1].ID}, nil); err != nil {
			return nil, err
		}
		c.settle(ctx, t, 15*time.Second)
		return c.observe(ctx, t, defaultText, "went back")
	})
}

// Screenshot saves the visible part of the page as a PNG and returns its path.
func (c *Controller) Screenshot(ctx context.Context) (string, *Outcome, error) {
	var path string
	out, err := c.run(ctx, false, func(ctx context.Context, t *tab) (*Outcome, error) {
		conn := c.connOrNil()
		var res struct {
			Data string `json:"data"`
		}
		if err := conn.call(ctx, t.session, "Page.captureScreenshot", map[string]any{"format": "png"}, &res); err != nil {
			return nil, err
		}
		png, err := base64.StdEncoding.DecodeString(res.Data)
		if err != nil || len(png) == 0 {
			return nil, errors.New("the browser returned no image")
		}
		if c.cfg.ShotDir == "" {
			return nil, errors.New("no screenshot folder is configured")
		}
		if err := os.MkdirAll(c.cfg.ShotDir, 0o755); err != nil {
			return nil, err
		}
		path = filepath.Join(c.cfg.ShotDir, "browser-"+time.Now().Format("20060102-150405.000")+".png")
		if err := os.WriteFile(path, png, 0o644); err != nil {
			return nil, err
		}
		pruneShots(c.cfg.ShotDir, 40)
		raw, _ := c.evalString(ctx, t, pageInfoScript)
		var pi struct{ URL, Title string }
		_ = json.Unmarshal([]byte(raw), &pi)
		return &Outcome{URL: pi.URL, Title: pi.Title, Message: fmt.Sprintf("saved a screenshot (%d bytes)", len(png))}, nil
	})
	return path, out, err
}

func pruneShots(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "browser-") && strings.HasSuffix(e.Name(), ".png") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for i := 0; i < len(names)-keep; i++ {
		_ = os.Remove(filepath.Join(dir, names[i]))
	}
}

// Close ends the browser this controller started, or closes the pages it
// opened in a browser it found already running.
func (c *Controller) Close(ctx context.Context) error {
	release, err := c.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	c.mu.Lock()
	conn, cmd, exited := c.conn, c.cmd, c.exited
	c.mu.Unlock()
	if conn == nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if cmd != nil {
		// We started this browser, so we end it. Its launcher may already be gone
		// (current Edge), so the end of the connection is what shows it closed.
		_ = conn.call(cctx, "", "Browser.close", nil, nil)
		select {
		case <-conn.Done():
		case <-exited:
		case <-time.After(3 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	} else {
		c.mu.Lock()
		var targets []string
		for _, t := range c.tabs {
			targets = append(targets, t.target)
		}
		c.mu.Unlock()
		for _, id := range targets {
			_ = conn.call(cctx, "", "Target.closeTarget", map[string]any{"targetId": id}, nil)
		}
	}
	if cmd != nil {
		_ = os.Remove(filepath.Join(c.cfg.ProfileDir, "DevToolsActivePort"))
	}
	c.reset()
	return nil
}
