// Package computeruse lets a vision model drive the local desktop: it sees a
// screenshot, then clicks, types and scrolls, and gets a fresh screenshot back
// after every action.
//
// The design follows Cursor's computer-use sidecar (see
// docs/cursor-cua-sidecar-reverse-2026-10-29.md): a typed error taxonomy with
// an escalation hint the model can act on, a "take a screenshot first" gate,
// a user-operated Stop that the model cannot cancel, and a named-pipe sidecar
// (go-winio) for the case where input has to be injected from another process.
// The Win32 capture/input substrate is adapted from octo-agent (MIT).
package computeruse

import (
	"errors"
	"fmt"
)

// Code is a stable machine-readable error identifier. The set mirrors the
// codes Cursor's sidecar returns, so prompts written for one work for both.
type Code string

const (
	CodeCaptureFailed      Code = "capture_failed"
	CodeInvalidArguments   Code = "invalid_arguments"
	CodeInputFailed        Code = "input_failed"
	CodeTimeout            Code = "timeout"
	CodeScreenshotRequired Code = "screenshot_required"
	CodeSessionBusy        Code = "session_busy"
	CodeUserAborted        Code = "user_aborted"
	CodeUnsupported        Code = "unsupported_request"
	CodeSecureDesktop      Code = "secure_desktop"
	CodeTargetElevated     Code = "target_elevated"
	CodeOutcomeUnknown     Code = "outcome_unknown"
	CodeSidecarUnavailable Code = "sidecar_unavailable"
)

// Escalation tells the model what to do next.
type Escalation string

const (
	// EscRetry: nothing was actuated (or it is safe); call again, fixing arguments.
	EscRetry Escalation = "retry"
	// EscAskUser: only the person at the machine can clear this.
	EscAskUser Escalation = "ask_user"
	// EscUseDifferentTool: run another action first (usually screenshot).
	EscUseDifferentTool Escalation = "use_different_tool"
	// EscStop: end computer use for this turn.
	EscStop Escalation = "stop"
)

var defaultEscalation = map[Code]Escalation{
	CodeCaptureFailed:      EscRetry,
	CodeInvalidArguments:   EscRetry,
	CodeInputFailed:        EscRetry,
	CodeTimeout:            EscRetry,
	CodeScreenshotRequired: EscUseDifferentTool,
	CodeOutcomeUnknown:     EscUseDifferentTool,
	CodeSessionBusy:        EscAskUser,
	CodeUnsupported:        EscAskUser,
	CodeSecureDesktop:      EscAskUser,
	CodeTargetElevated:     EscAskUser,
	CodeSidecarUnavailable: EscAskUser,
	CodeUserAborted:        EscStop,
}

var defaultReason = map[Escalation]string{
	EscRetry:            "Nothing was actuated; fix the arguments if the message says so and call again.",
	EscAskUser:          "Only the person at the machine can clear this; tell them what the message says.",
	EscUseDifferentTool: "Take a screenshot first, then repeat the action with coordinates from that image.",
	EscStop:             "The user stopped the session. Do not call computer_use again this turn.",
}

// Error is the structured failure every action returns. It is JSON-encodable
// so it crosses the sidecar pipe and reaches the model unchanged.
type Error struct {
	Code       Code       `json:"code"`
	Message    string     `json:"message"`
	Escalation Escalation `json:"escalation"`
	Reason     string     `json:"reason,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// NewError builds an Error with the default escalation for code.
func NewError(code Code, format string, args ...any) *Error {
	esc, ok := defaultEscalation[code]
	if !ok {
		esc = EscRetry
	}
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Escalation: esc, Reason: defaultReason[esc]}
}

// AsError converts any error into an *Error (unknown errors become input_failed).
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return NewError(CodeInputFailed, "%v", err)
}

// ErrUnsupported marks a build or platform with no desktop substrate.
var ErrUnsupported = errors.New("computer-use is only available on Windows")
