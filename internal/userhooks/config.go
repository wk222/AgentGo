// Package userhooks runs the scripts a user lists in hooks.json at fixed points
// of a run: before and after a tool call, when a message is submitted, and when
// a turn ends. A script can refuse a tool call or a message, add context to a
// message, or just record what happened.
//
// The file has the shape of Claude Code's hook settings, so scripts written for
// it keep working:
//
//	{"hooks": {"PreToolUse": [
//	  {"matcher": "Bash", "hooks": [{"type": "command", "command": "python guard.py", "timeout": 10}]}
//	]}}
//
// A script gets one JSON document on stdin and answers with its exit status:
// 0 lets things go on (stdout may carry a JSON decision), 2 refuses (stderr is
// the reason shown to the model), anything else is a failure of the script
// itself, which by default lets things go on and is only reported.
//
// The package knows nothing about tools, sessions or the screen: a caller fires
// an Event and gets an Outcome back.
package userhooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The events a hook can be attached to.
const (
	PreToolUse       = "PreToolUse"       // about to run a tool; the hook may refuse
	PostToolUse      = "PostToolUse"      // a tool has run; observation only
	UserPromptSubmit = "UserPromptSubmit" // the user sent a message; the hook may refuse it or add context
	Stop             = "Stop"             // a turn ended; observation only
)

// Events lists the supported events in the order a screen shows them.
var Events = []string{PreToolUse, PostToolUse, UserPromptSubmit, Stop}

func isToolEvent(name string) bool { return name == PreToolUse || name == PostToolUse }

func knownEvent(name string) bool {
	for _, e := range Events {
		if e == name {
			return true
		}
	}
	return false
}

// Limits. Each is a bound a runaway script must not be able to exceed.
const (
	DefaultTimeout   = 30 * time.Second
	MaxTimeout       = 600 * time.Second
	OutputCap        = 64 << 10 // bytes kept of each of a script's stdout and stderr
	MaxHooksPerEvent = 20       // hooks beyond this are left out and reported
)

// What to do when the script itself fails (cannot start, crashes, times out).
const (
	OnFailureContinue = "continue"
	OnFailureBlock    = "block"
)

// Hook is one script attached to one event.
type Hook struct {
	Event     string
	Matcher   string // as written; empty = every tool
	Command   string
	Timeout   time.Duration
	OnFailure string // OnFailureContinue or OnFailureBlock

	match matcher
}

// id names the hook for the lock that keeps one script from running twice at once.
func (h Hook) id() string { return h.Event + "\x00" + h.Matcher + "\x00" + h.Command }

// Label is a short text that tells hooks apart in messages and records.
func (h Hook) Label() string {
	cmd := strings.Join(strings.Fields(h.Command), " ")
	if r := []rune(cmd); len(r) > 60 {
		cmd = string(r[:60]) + "…"
	}
	if isToolEvent(h.Event) && h.Matcher != "" {
		return fmt.Sprintf("%s[%s] %s", h.Event, h.Matcher, cmd)
	}
	return fmt.Sprintf("%s %s", h.Event, cmd)
}

// Config is what a hooks.json file says. It is never changed after Parse.
type Config struct {
	hooks map[string][]Hook
}

// For returns the hooks of one event in the order the file lists them.
func (c Config) For(event string) []Hook { return c.hooks[event] }

// Count is the number of hooks over all events.
func (c Config) Count() int {
	n := 0
	for _, hs := range c.hooks {
		n += len(hs)
	}
	return n
}

// Counts is the number of hooks per supported event.
func (c Config) Counts() map[string]int {
	out := make(map[string]int, len(Events))
	for _, e := range Events {
		out[e] = len(c.hooks[e])
	}
	return out
}

// Problem is something in hooks.json that cannot be used as written. The rest of the
// file still applies.
type Problem struct {
	Where   string `json:"where"`
	Message string `json:"message"`
}

func (p Problem) String() string { return p.Where + ": " + p.Message }

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

type rawGroup struct {
	Matcher string    `json:"matcher"`
	Hooks   []rawHook `json:"hooks"`
}

type rawHook struct {
	Type      string   `json:"type"`
	Command   string   `json:"command"`
	Timeout   *float64 `json:"timeout"` // seconds
	OnFailure string   `json:"onFailure"`
	Async     bool     `json:"async"`
}

// Parse reads the text of a hooks.json. Text that is not JSON at all is an
// error; an entry that cannot be used is left out and reported as a Problem, so
// one typo does not switch off every other hook. An empty file is a valid file
// without hooks.
func Parse(data []byte) (Config, []Problem, error) {
	data = bytes.TrimPrefix(data, utf8BOM) // Notepad writes one
	if len(bytes.TrimSpace(data)) == 0 {
		return Config{}, nil, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return Config{}, nil, fmt.Errorf("hooks.json is not valid JSON: %w", err)
	}
	var problems []Problem
	if raw, ok := top["disableAllHooks"]; ok {
		var off bool
		if json.Unmarshal(raw, &off) == nil && off {
			return Config{}, []Problem{{Where: "disableAllHooks", Message: "is true: every hook is switched off"}}, nil
		}
	}

	events := top
	prefix := ""
	if raw, ok := top["hooks"]; ok {
		events = nil
		if err := json.Unmarshal(raw, &events); err != nil {
			return Config{}, nil, fmt.Errorf(`"hooks" must be an object that maps event names to lists`)
		}
		prefix = "hooks."
	}

	names := make([]string, 0, len(events))
	for name := range events {
		names = append(names, name)
	}
	sort.Strings(names)

	cfg := Config{hooks: map[string][]Hook{}}
	sawEvent := false
	for _, name := range names {
		if !knownEvent(name) {
			if prefix != "" { // a bare top-level file may carry other settings
				problems = append(problems, Problem{Where: prefix + name, Message: fmt.Sprintf(
					"event %q is not supported; supported: %s", name, strings.Join(Events, ", "))})
			}
			continue
		}
		sawEvent = true
		hooks, ps := parseEvent(prefix+name, name, events[name])
		cfg.hooks[name] = hooks
		problems = append(problems, ps...)
	}
	if prefix == "" && len(names) > 0 && !sawEvent {
		problems = append(problems, Problem{Where: "hooks.json", Message: `no hooks found; expected {"hooks": {"PreToolUse": [{"matcher": "...", "hooks": [{"type": "command", "command": "..."}]}]}}`})
	}
	return cfg, problems, nil
}

func parseEvent(where, event string, raw json.RawMessage) ([]Hook, []Problem) {
	var groups []rawGroup
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, []Problem{{Where: where, Message: `expected a list of {"matcher", "hooks"} groups`}}
	}
	var (
		out      []Hook
		problems []Problem
	)
	for gi, g := range groups {
		gw := fmt.Sprintf("%s[%d]", where, gi)
		m := matcher{all: true}
		if isToolEvent(event) {
			var err error
			if m, err = compileMatcher(g.Matcher); err != nil {
				problems = append(problems, Problem{Where: gw + ".matcher", Message: err.Error()})
				continue
			}
		} else if s := strings.TrimSpace(g.Matcher); s != "" && s != "*" {
			problems = append(problems, Problem{Where: gw + ".matcher", Message: event + " has no tool name to match; the matcher is ignored"})
		}
		for hi, rh := range g.Hooks {
			hw := fmt.Sprintf("%s.hooks[%d]", gw, hi)
			h, ps, ok := buildHook(hw, event, g.Matcher, m, rh)
			problems = append(problems, ps...)
			if !ok {
				continue
			}
			if len(out) >= MaxHooksPerEvent {
				problems = append(problems, Problem{Where: hw, Message: fmt.Sprintf("more than %d hooks for %s; the rest are left out", MaxHooksPerEvent, event)})
				return out, problems
			}
			out = append(out, h)
		}
	}
	return out, problems
}

func buildHook(where, event, matcherText string, m matcher, rh rawHook) (Hook, []Problem, bool) {
	var problems []Problem
	if t := strings.TrimSpace(rh.Type); t != "" && t != "command" {
		return Hook{}, []Problem{{Where: where + ".type", Message: fmt.Sprintf(`type %q is not supported; only "command" is`, t)}}, false
	}
	cmd := strings.TrimSpace(rh.Command)
	if cmd == "" {
		return Hook{}, []Problem{{Where: where + ".command", Message: "is empty"}}, false
	}
	h := Hook{Event: event, Matcher: strings.TrimSpace(matcherText), Command: cmd, Timeout: DefaultTimeout, OnFailure: OnFailureContinue, match: m}
	if !isToolEvent(event) {
		h.Matcher = ""
	}
	if rh.Timeout != nil && *rh.Timeout > 0 {
		h.Timeout = time.Duration(*rh.Timeout * float64(time.Second))
		if h.Timeout > MaxTimeout {
			h.Timeout = MaxTimeout
			problems = append(problems, Problem{Where: where + ".timeout", Message: fmt.Sprintf("is longer than %d seconds; using %d", int(MaxTimeout/time.Second), int(MaxTimeout/time.Second))})
		}
	}
	switch strings.ToLower(strings.TrimSpace(rh.OnFailure)) {
	case "", OnFailureContinue:
	case OnFailureBlock:
		h.OnFailure = OnFailureBlock
	default:
		problems = append(problems, Problem{Where: where + ".onFailure", Message: fmt.Sprintf(`%q is not "continue" or "block"; using "continue"`, rh.OnFailure)})
	}
	if rh.Async {
		problems = append(problems, Problem{Where: where + ".async", Message: "is not supported yet; the hook runs and is waited for"})
	}
	return h, problems, true
}
