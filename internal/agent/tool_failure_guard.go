package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"syscall"
)

// Two things the model should not have to work out for itself after a tool fails:
//
//  1. Repeating the same call with the same arguments and getting the same failure is a loop. The third
//     identical failure in a row gets one extra hint to change approach; the sixth is refused without
//     running the tool. A success, or a call with different arguments, starts counting again, so a
//     reasonable fix is never locked out.
//  2. A failure that happened after the request may already have left (connection reset, timeout, EOF)
//     says nothing about whether the other side acted. The result says so, so the model checks the
//     state before it sends, writes or creates the same thing again. A failure that happened before
//     anything ran ("execution was not started") is not marked, because retrying that is safe.

const (
	sameFailureHint   = 3
	sameFailureRefuse = 6
)

type failureGuard struct {
	mu     sync.Mutex
	counts map[string]int
}

func newFailureGuard() *failureGuard { return &failureGuard{counts: map[string]int{}} }

func failureKey(tool, args string) string {
	canon := args
	var v any
	if json.Unmarshal([]byte(args), &v) == nil {
		if b, err := json.Marshal(v); err == nil { // maps marshal with sorted keys
			canon = string(b)
		}
	}
	sum := sha256.Sum256([]byte(canon))
	return tool + "|" + hex.EncodeToString(sum[:8])
}

// before returns a refusal when the same call has failed too often in a row.
func (g *failureGuard) before(tool, args string) (string, bool) {
	if g == nil || tool == "" {
		return "", false
	}
	g.mu.Lock()
	n := g.counts[failureKey(tool, args)]
	g.mu.Unlock()
	if n >= sameFailureRefuse {
		return "[stop-loss] This exact call to " + tool + " has failed " + itoa(n) +
			" times in a row, so it was not run again. Change the arguments or the approach, " +
			"or tell the user what is blocking you.", true
	}
	return "", false
}

// after records the outcome and returns the text to hand to the model.
func (g *failureGuard) after(tool, args, result string, failed bool) string {
	if g == nil || tool == "" {
		return result
	}
	key := failureKey(tool, args)
	g.mu.Lock()
	defer g.mu.Unlock()
	if !failed {
		delete(g.counts, key)
		return result
	}
	g.counts[key]++
	if g.counts[key] == sameFailureHint {
		return result + "\n\n[hint] The same call to " + tool + " has now failed " + itoa(sameFailureHint) +
			" times in a row with identical arguments. Do not repeat it unchanged: change the arguments, " +
			"use another tool, or tell the user what is blocking you."
	}
	return result
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// resultFailed reports whether a tool result text describes a failure: the "[tool error]" text the safe
// middleware writes, or a JSON object with success=false or a non-empty error.
func resultFailed(s string) bool {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "[tool error") || strings.HasPrefix(t, "[stop-loss]") {
		return true
	}
	if len(t) == 0 || t[0] != '{' || len(t) > 64*1024 {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(t), &m) != nil {
		return false
	}
	if raw, ok := m["success"]; ok {
		var b bool
		if json.Unmarshal(raw, &b) == nil && !b {
			return true
		}
	}
	if raw, ok := m["error"]; ok {
		var e string
		if json.Unmarshal(raw, &e) == nil && strings.TrimSpace(e) != "" {
			return true
		}
	}
	return false
}

// outcomeUnknown reports whether err happened after the request may have been sent, so the real outcome
// on the other side is not known.
func outcomeUnknown(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "was not started") {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	for _, s := range []string{
		"connection reset", "broken pipe", "unexpected eof", "i/o timeout", "client.timeout", "timed out", "deadline exceeded",
		"connection closed", "transport is closing", "forcibly closed", "server closed",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// failureText is the text the model sees for a tool error.
func failureText(err error) string {
	if outcomeUnknown(err) {
		return "[tool error, outcome unknown] " + err.Error() +
			"\nThe request may already have been carried out on the other side. Check the current state " +
			"(read, list or query it) before repeating anything that sends, writes, creates or pays."
	}
	return "[tool error] " + err.Error()
}
