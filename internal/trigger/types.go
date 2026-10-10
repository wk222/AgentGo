// Package trigger turns external events (a process exiting, a marker file appearing, a log line
// matching, a webhook) into agent wake-ups, without any polling of the model.
package trigger

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type Kind string

const (
	KindProcessExit Kind = "process_exit"
	KindFile        Kind = "file"      // marker file appears
	KindLogMatch    Kind = "log_match" // a new line in a log file matches Pattern
	KindWebhook     Kind = "webhook"   // fired by POST /api/v1/triggers/{id}/fire
)

type Status string

const (
	StatusArmed     Status = "armed"
	StatusFired     Status = "fired"
	StatusCancelled Status = "cancelled"
	StatusExpired   Status = "expired"
)

// Action names what happens when the trigger fires.
const (
	ActionWakeSession = "wake_session" // start a background agent turn in SessionID with Prompt + event
	ActionEmitSignal  = "emit_signal"  // deliver Signal to workflow run RunID
)

type Trigger struct {
	ID        string `json:"id"`
	Kind      Kind   `json:"kind"`
	Title     string `json:"title,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Path      string `json:"path,omitempty"`
	Pattern   string `json:"pattern,omitempty"`
	Action    string `json:"action"`
	SessionID string `json:"session_id,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	Signal    string `json:"signal,omitempty"`
	Status    Status `json:"status"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at,omitempty"` // unix seconds, 0 = never
	FiredAt   int64  `json:"fired_at,omitempty"`
	Delivered bool   `json:"delivered"`
	Result    string `json:"result,omitempty"` // event detail JSON, or the delivery error
}

// Event is what a watcher reports when its condition is met.
type Event struct {
	TriggerID string         `json:"trigger_id"`
	Kind      Kind           `json:"kind"`
	At        int64          `json:"at"`
	Detail    map[string]any `json:"detail,omitempty"`
}

// Validate checks a trigger definition before it is stored or armed.
func (t *Trigger) Validate() error {
	switch t.Kind {
	case KindProcessExit:
		if t.PID <= 0 {
			return fmt.Errorf("process_exit requires pid > 0")
		}
	case KindFile:
		if err := requireAbs(t.Path); err != nil {
			return err
		}
	case KindLogMatch:
		if err := requireAbs(t.Path); err != nil {
			return err
		}
		if strings.TrimSpace(t.Pattern) == "" {
			return fmt.Errorf("log_match requires pattern")
		}
		if _, err := regexp.Compile(t.Pattern); err != nil {
			return fmt.Errorf("invalid pattern: %w", err)
		}
	case KindWebhook:
	default:
		return fmt.Errorf("unknown trigger kind %q", t.Kind)
	}
	switch t.Action {
	case ActionWakeSession:
		if strings.TrimSpace(t.SessionID) == "" {
			return fmt.Errorf("wake_session requires session_id")
		}
	case ActionEmitSignal:
		if strings.TrimSpace(t.RunID) == "" || strings.TrimSpace(t.Signal) == "" {
			return fmt.Errorf("emit_signal requires run_id and signal")
		}
	default:
		return fmt.Errorf("unknown action %q", t.Action)
	}
	return nil
}

func requireAbs(p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path must be absolute: %q", p)
	}
	return nil
}
