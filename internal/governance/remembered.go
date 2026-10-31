package governance

import (
	"fmt"
	"strings"
	"sync"
)

// "Always allow this" decisions (the idea comes from octo-agent's
// permission.Remembered). An approval the user already gave should not be asked
// again for the same thing within the same conversation.
//
// Scope is deliberately narrow:
//   - per session: a decision never leaks into another conversation;
//   - in memory: it dies with the process, so a restart re-asks;
//   - two grains: ScopeCall remembers one exact tool call (same arguments),
//     ScopeTool remembers every call of that tool.
//
// Some tools can never be remembered at tool grain because one approval would
// hand over too much (a shell, the elevated runner, computer control); they
// still allow ScopeCall. CRITICAL-risk calls and calls that grant access to
// folders outside the workspace are never short-circuited by this store (the
// check in InvokeWithPolicy skips it for them).

// RememberScope is how widely an approval is remembered.
type RememberScope string

const (
	ScopeCall RememberScope = "call"
	ScopeTool RememberScope = "tool"
)

// neverRememberToolWide lists tools whose approval must stay per call.
var neverRememberToolWide = map[string]bool{
	"execute_bash": true,
	"run_elevated": true,
	"computer_use": true,
}

func init() { neverRememberToolWide[ElevatedTool] = true }

type rememberedStore struct {
	mu    sync.Mutex
	calls map[string]struct{} // session \x00 payload hash
	tools map[string]struct{} // session \x00 tool name
}

var remembered = &rememberedStore{calls: map[string]struct{}{}, tools: map[string]struct{}{}}

func rememberKey(sessionID, part string) string { return sessionID + "\x00" + part }

// ParseRememberScope maps user input to a scope; "" means ScopeCall.
func ParseRememberScope(s string) (RememberScope, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "call", "once", "same":
		return ScopeCall, nil
	case "tool", "session", "always":
		return ScopeTool, nil
	}
	return "", fmt.Errorf("unknown scope %q (call or tool)", s)
}

// RememberApproval records that the user approved this call and wants the same
// kind of call let through without asking for the rest of the session.
func RememberApproval(sessionID, toolName, argumentsJSON string, scope RememberScope) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("cannot remember an approval without a session")
	}
	if strings.TrimSpace(toolName) == "" {
		return fmt.Errorf("tool name is required")
	}
	remembered.mu.Lock()
	defer remembered.mu.Unlock()
	switch scope {
	case ScopeTool:
		if neverRememberToolWide[toolName] {
			return fmt.Errorf("%s cannot be allowed for every call: one approval would be too broad. Remember just this call instead", toolName)
		}
		remembered.tools[rememberKey(sessionID, toolName)] = struct{}{}
	case ScopeCall:
		remembered.calls[rememberKey(sessionID, ComputePayloadHash(toolName, argumentsJSON))] = struct{}{}
	default:
		return fmt.Errorf("unknown remember scope %q", scope)
	}
	return nil
}

// IsRemembered reports whether the user already approved this call.
func IsRemembered(sessionID, toolName, argumentsJSON string) bool {
	if strings.TrimSpace(sessionID) == "" {
		return false
	}
	remembered.mu.Lock()
	defer remembered.mu.Unlock()
	if _, ok := remembered.tools[rememberKey(sessionID, toolName)]; ok && !neverRememberToolWide[toolName] {
		return true
	}
	_, ok := remembered.calls[rememberKey(sessionID, ComputePayloadHash(toolName, argumentsJSON))]
	return ok
}

// ForgetRemembered drops everything remembered for a session (new chat,
// policy change, "revoke" in the UI).
func ForgetRemembered(sessionID string) {
	prefix := sessionID + "\x00"
	remembered.mu.Lock()
	defer remembered.mu.Unlock()
	for k := range remembered.calls {
		if strings.HasPrefix(k, prefix) {
			delete(remembered.calls, k)
		}
	}
	for k := range remembered.tools {
		if strings.HasPrefix(k, prefix) {
			delete(remembered.tools, k)
		}
	}
}

// RememberedTools lists the tools allowed session-wide (for a settings view).
func RememberedTools(sessionID string) []string {
	prefix := sessionID + "\x00"
	remembered.mu.Lock()
	defer remembered.mu.Unlock()
	var out []string
	for k := range remembered.tools {
		if strings.HasPrefix(k, prefix) {
			out = append(out, strings.TrimPrefix(k, prefix))
		}
	}
	return out
}
