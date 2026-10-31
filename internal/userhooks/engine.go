package userhooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// EnvInHook is set for every hook script. An agentgo started by a script sees
// it and runs no hooks of its own, so a hook cannot start itself again.
const EnvInHook = "AGENTGO_IN_HOOK"

const (
	// MaxRuns is how many finished runs the engine remembers for a screen to show.
	MaxRuns = 200

	maxReasonChars  = 2000 // longest refusal text handed back to the model
	maxRecordChars  = 500  // longest reason kept in a run record
	maxPayloadBytes = 64 << 10
)

// Event is one thing that happened, as the hook scripts are told about it.
type Event struct {
	Name       string // one of Events
	SessionID  string
	Cwd        string // the workspace root; scripts run there
	ToolName   string // PreToolUse, PostToolUse
	ToolInput  string // the tool's arguments, as the JSON text the model gave
	ToolUseID  string
	ToolOutput string // PostToolUse: what the tool returned
	ToolError  string // PostToolUse: why it failed, if it did
	Prompt     string // UserPromptSubmit: the user's message
	LastReply  string // Stop: the last answer of the turn
}

// Outcome is what the hooks of one event decided together.
type Outcome struct {
	Blocked  bool     // a hook refused; meaningful for PreToolUse and UserPromptSubmit only
	Reason   string   // why, in the script's words; set when Blocked
	By       string   // the label of the hook that refused
	Context  string   // text to give the model along with the message (UserPromptSubmit)
	Warnings []string // scripts that failed and were let through, and answers that were ignored
}

// Run is the record of one hook script having run.
type Run struct {
	Seq        int64     `json:"seq"`
	At         time.Time `json:"at"`
	Event      string    `json:"event"`
	Tool       string    `json:"tool,omitempty"`
	Hook       string    `json:"hook"`
	DurationMs int64     `json:"duration_ms"`
	ExitCode   int       `json:"exit_code"`
	Result     string    `json:"result"` // ok, blocked, failed, timeout or canceled
	Reason     string    `json:"reason,omitempty"`
}

// HookInfo is one configured hook as a settings screen lists it.
type HookInfo struct {
	Event     string `json:"event"`
	Matcher   string `json:"matcher,omitempty"`
	Command   string `json:"command"`
	TimeoutS  int    `json:"timeout_s"`
	OnFailure string `json:"on_failure"`
}

// Status is what a settings screen shows.
type Status struct {
	Enabled        bool           `json:"enabled"`
	DisabledReason string         `json:"disabled_reason,omitempty"`
	Path           string         `json:"path"`
	Exists         bool           `json:"exists"`
	LoadedAt       string         `json:"loaded_at,omitempty"`
	SHA256         string         `json:"sha256,omitempty"`
	Counts         map[string]int `json:"counts"`
	Total          int            `json:"total"`
	Hooks          []HookInfo     `json:"hooks"`
	Problems       []Problem      `json:"problems"`
	Error          string         `json:"error,omitempty"` // the file could not be read; the last good one stays in use
	Recent         []Run          `json:"recent"`          // newest first
}

// Options configure an Engine.
type Options struct {
	// Path is the hooks.json to read. It need not exist.
	Path string
	// Aliases maps the tool names a hook author may know from other products
	// (Claude's "Bash", "Edit") to the names this app gives those tools.
	Aliases map[string][]string
	// CheckEvery is how often the file is looked at for changes; 0 means before every use.
	CheckEvery time.Duration
}

type fileSig struct {
	mod  int64
	size int64
}

// Engine reads hooks.json and runs its scripts. Safe for concurrent use.
//
// The file is read again when its time or size changes, so an edit takes effect
// without a restart. A file that cannot be parsed does not take the hooks away:
// the last good one stays in use and the error is reported.
type Engine struct {
	path       string
	aliasOf    map[string][]string // this app's tool name -> the other names a matcher may use
	checkEvery time.Duration
	off        string // why the engine does nothing, when it does not

	mu       sync.Mutex
	cfg      Config
	sig      fileSig
	checked  time.Time
	loadedAt time.Time
	sum      string
	problems []Problem
	lastErr  string
	locks    map[string]chan struct{}
	runs     []Run
	seq      int64
}

// New returns an engine for the hooks.json at opts.Path.
func New(opts Options) *Engine {
	e := &Engine{
		path:       opts.Path,
		checkEvery: opts.CheckEvery,
		aliasOf:    map[string][]string{},
		locks:      map[string]chan struct{}{},
	}
	for other, ours := range opts.Aliases {
		for _, name := range ours {
			e.aliasOf[name] = append(e.aliasOf[name], other)
		}
	}
	if os.Getenv(EnvInHook) == "1" {
		e.off = "this process was started by a hook script"
	}
	return e
}

// refresh reads hooks.json again if it changed since the last look.
func (e *Engine) refresh(force bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !force && e.checkEvery > 0 && !e.checked.IsZero() && time.Since(e.checked) < e.checkEvery {
		return
	}
	e.checked = time.Now()

	fi, err := os.Stat(e.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if e.sig != (fileSig{}) {
				log.Printf("[userhooks] %s was removed: no hooks", e.path)
			}
			e.cfg, e.sig, e.sum, e.problems, e.lastErr = Config{}, fileSig{}, "", nil, ""
			return
		}
		e.lastErr = err.Error()
		return
	}
	sig := fileSig{mod: fi.ModTime().UnixNano(), size: fi.Size()}
	if sig == e.sig && !force {
		return
	}
	data, err := os.ReadFile(e.path)
	if err != nil {
		e.lastErr = err.Error() // the signature stays, so the next look tries again
		return
	}
	e.sig = sig
	cfg, problems, err := Parse(data)
	if err != nil {
		e.lastErr = err.Error()
		log.Printf("[userhooks] %s not loaded, keeping the previous hooks: %v", e.path, err)
		return
	}
	sum := sha256.Sum256(data)
	e.cfg, e.problems, e.lastErr, e.sum, e.loadedAt = cfg, problems, "", hex.EncodeToString(sum[:]), time.Now()
	log.Printf("[userhooks] %s loaded: %d hook(s), %d problem(s), sha256=%s", e.path, cfg.Count(), len(problems), e.sum)
}

// Reload looks at the file now and returns the resulting status.
func (e *Engine) Reload() Status {
	if e != nil {
		e.refresh(true)
	}
	return e.Status()
}

// Status describes the engine for a screen: where the file is, what it holds,
// what is wrong with it, and what ran lately.
func (e *Engine) Status() Status {
	if e == nil {
		return Status{Counts: map[string]int{}, Hooks: []HookInfo{}, Problems: []Problem{}, Recent: []Run{}}
	}
	e.refresh(false)
	e.mu.Lock()
	defer e.mu.Unlock()
	st := Status{
		Enabled: e.off == "", DisabledReason: e.off, Path: e.path, Exists: e.sig != (fileSig{}) || e.lastErr != "",
		SHA256: e.sum, Counts: e.cfg.Counts(), Total: e.cfg.Count(), Error: e.lastErr, Hooks: []HookInfo{},
		Problems: append([]Problem{}, e.problems...), Recent: make([]Run, 0, len(e.runs)),
	}
	if !e.loadedAt.IsZero() {
		st.LoadedAt = e.loadedAt.Format(time.RFC3339)
	}
	for _, event := range Events {
		for _, h := range e.cfg.For(event) {
			st.Hooks = append(st.Hooks, HookInfo{
				Event: event, Matcher: h.Matcher, Command: h.Command,
				TimeoutS: int(h.Timeout.Seconds() + 0.5), OnFailure: h.OnFailure,
			})
		}
	}
	for i := len(e.runs) - 1; i >= 0; i-- {
		st.Recent = append(st.Recent, e.runs[i])
	}
	return st
}

// Has reports whether any hook is configured for the event, so a caller can
// skip work (a goroutine, a payload) that would only be thrown away.
func (e *Engine) Has(event string) bool {
	if e == nil || e.off != "" {
		return false
	}
	e.refresh(false)
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.cfg.For(event)) > 0
}

// Fire runs the hooks that apply to ev, in the order the file lists them, and
// stops at the first that refuses. With no hook configured for the event it
// returns at once and starts nothing.
//
// A hook script that fails (cannot start, crashes, times out) lets things go on
// and is reported in Warnings, unless the hook says "onFailure": "block".
// Cancelling ctx ends the running script and skips the rest.
func (e *Engine) Fire(ctx context.Context, ev Event) Outcome {
	var out Outcome
	if e == nil || e.off != "" {
		return out
	}
	e.refresh(false)
	hooks := e.pick(ev)
	if len(hooks) == 0 {
		return out
	}
	stdin, env := payloadFor(ev)
	var contexts []string
	for _, h := range hooks {
		if ctx.Err() != nil {
			break
		}
		r := e.runHook(ctx, h, ev, stdin, env)
		if r.canceled {
			break
		}
		out.Warnings = append(out.Warnings, r.warnings...)
		if r.context != "" {
			contexts = append(contexts, r.context)
		}
		if r.blocked {
			out.Blocked, out.Reason, out.By = true, r.reason, h.Label()
			break
		}
	}
	out.Context = strings.Join(contexts, "\n")
	return out
}

// pick selects the hooks of ev's event that apply to its tool.
func (e *Engine) pick(ev Event) []Hook {
	e.mu.Lock()
	hooks := e.cfg.For(ev.Name)
	e.mu.Unlock()
	if len(hooks) == 0 || !isToolEvent(ev.Name) {
		return hooks
	}
	names := append([]string{ev.ToolName}, e.aliasOf[ev.ToolName]...)
	var out []Hook
	for _, h := range hooks {
		if h.match.matches(names) {
			out = append(out, h)
		}
	}
	return out
}

type hookResult struct {
	blocked  bool
	reason   string
	context  string
	warnings []string
	canceled bool
}

// runHook runs one hook script and turns what happened into a result and a record.
func (e *Engine) runHook(ctx context.Context, h Hook, ev Event, stdin []byte, env []string) hookResult {
	release, ok := e.acquire(ctx, h.id())
	if !ok {
		return hookResult{canceled: true}
	}
	defer release()

	res := runScript(ctx, h, ev.Cwd, env, stdin)
	rec := Run{At: time.Now(), Event: ev.Name, Tool: ev.ToolName, Hook: h.Label(), DurationMs: res.Elapsed.Milliseconds(), ExitCode: res.Code}
	var r hookResult
	canRefuse := ev.Name == PreToolUse || ev.Name == UserPromptSubmit

	// failed handles a script that did not do its job: it lets things go on
	// unless the hook asked to be strict.
	failed := func(result, why string) {
		rec.Result, rec.Reason = result, why
		if h.OnFailure == OnFailureBlock && canRefuse {
			r.blocked, r.reason = true, why+" (this hook is set to block when it fails)"
			return
		}
		r.warnings = append(r.warnings, fmt.Sprintf("%s: %s; let through", h.Label(), why))
	}

	switch {
	case res.Canceled:
		rec.Result = "canceled"
		r.canceled = true
	case res.TimedOut:
		rec.ExitCode = -1
		failed("timeout", fmt.Sprintf("timed out after %d seconds", int(h.Timeout/time.Second)))
	case res.Err != nil:
		failed("failed", "could not run: "+res.Err.Error())
	case res.Code == 0:
		v := readVerdict(ev.Name, res.Stdout)
		r.context = v.context
		for _, n := range v.notes {
			r.warnings = append(r.warnings, h.Label()+": "+n)
		}
		rec.Result = "ok"
		if v.block {
			rec.Result = "blocked"
			r.blocked, r.reason = true, orDefault(v.reason, "refused by the hook without a reason")
		}
	case res.Code == 2:
		reason := orDefault(tidy(res.Stderr), "refused by the hook (exit status 2) without a reason")
		if canRefuse {
			rec.Result = "blocked"
			r.blocked, r.reason = true, reason
		} else {
			rec.Result = "ok"
			r.warnings = append(r.warnings, fmt.Sprintf("%s: exit status 2 after %s cannot change what happened; recorded only", h.Label(), ev.Name))
		}
		rec.Reason = reason
	default:
		failed("failed", fmt.Sprintf("exit status %d%s", res.Code, tail(tidy(res.Stderr))))
	}

	if r.blocked {
		rec.Reason = r.reason
		r.reason = clip(r.reason, maxReasonChars)
	}
	rec.Reason = clip(rec.Reason, maxRecordChars)
	if rec.Reason == "" && len(r.warnings) > 0 {
		rec.Reason = clip(r.warnings[0], maxRecordChars)
	}
	e.record(rec)
	return r
}

func (e *Engine) record(r Run) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	r.Seq = e.seq
	e.runs = append(e.runs, r)
	if len(e.runs) > MaxRuns {
		e.runs = append(e.runs[:0], e.runs[len(e.runs)-MaxRuns:]...)
	}
}

// acquire takes the lock of one hook, so a script that is not built to run twice
// at once (parallel tool calls fire the same hook together) never does.
func (e *Engine) acquire(ctx context.Context, id string) (release func(), ok bool) {
	e.mu.Lock()
	ch := e.locks[id]
	if ch == nil {
		ch = make(chan struct{}, 1)
		e.locks[id] = ch
	}
	e.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	case <-ctx.Done():
		return nil, false
	}
}

// payloadFor builds the JSON a script reads on stdin and the environment it runs
// in. The fields are the common subset of Claude Code's and Codex's; there is no
// transcript file in this app, so transcript_path is always null.
func payloadFor(ev Event) (stdin []byte, env []string) {
	m := map[string]any{
		"session_id":      ev.SessionID,
		"transcript_path": nil,
		"cwd":             ev.Cwd,
		"hook_event_name": ev.Name,
	}
	switch ev.Name {
	case PreToolUse, PostToolUse:
		m["tool_name"] = ev.ToolName
		m["tool_input"] = toolInput(ev.ToolInput)
		if ev.ToolUseID != "" {
			m["tool_use_id"] = ev.ToolUseID
		}
		if ev.Name == PostToolUse {
			m["tool_response"] = clip(ev.ToolOutput, maxPayloadBytes)
			if ev.ToolError != "" {
				m["tool_error"] = clip(ev.ToolError, maxPayloadBytes)
			}
		}
	case UserPromptSubmit:
		m["prompt"] = ev.Prompt
	case Stop:
		m["stop_hook_active"] = false
		m["last_assistant_message"] = clip(ev.LastReply, maxPayloadBytes)
	}
	stdin, _ = json.Marshal(m)
	env = []string{
		"AGENTGO_PROJECT_DIR=" + ev.Cwd,
		"CLAUDE_PROJECT_DIR=" + ev.Cwd,
		"AGENTGO_HOOK_EVENT=" + ev.Name,
		"AGENTGO_SESSION_ID=" + ev.SessionID,
		EnvInHook + "=1",
	}
	return stdin, env
}

// toolInput gives the arguments as the JSON value they are; text that is not
// JSON is passed on as a string.
func toolInput(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var v any
	if json.Unmarshal([]byte(raw), &v) == nil {
		return v
	}
	return raw
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// tidy makes a script's output fit one message: valid UTF-8, no carriage returns, trimmed.
func tidy(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
}

func tail(s string) string {
	if s == "" {
		return ""
	}
	return ": " + clip(strings.Join(strings.Fields(s), " "), 200)
}

// clip cuts s to at most max bytes, never inside a character.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
