package applog

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

const (
	crashFileName     = "crash.log"
	crashLastFileName = "crash.last.log"
	crashHeadBytes    = 4096
	crashHeadlineMax  = 200
)

// CrashInfo describes the crash the previous run ended with.
type CrashInfo struct {
	// File is where the crash output was moved to (it stays until the next crash replaces it).
	File string `json:"file"`
	// Time is when the crash output was last written, that is, when the app went down.
	Time time.Time `json:"time"`
	// Size is the size of the crash output in bytes.
	Size int64 `json:"size"`
	// Headline is the first meaningful line, e.g. "panic: runtime error: index out of range".
	Headline string `json:"headline"`
}

var previousCrash *CrashInfo // guarded by mu

// InitCrash makes the Go runtime copy fatal error output into dataDir/crash.log. That covers what no recover()
// can catch, a panic in a goroutine nobody guards or "concurrent map writes", and it matters most in the desktop
// build, which has no console, so such a crash would otherwise leave no trace at all.
//
// A crash.log with content is the previous run's crash: it is moved to crash.last.log, reported by
// PreviousCrash and a fresh, empty crash.log takes its place. A run that ends normally leaves it empty.
func InitCrash(dataDir string) error {
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	cur := filepath.Join(dataDir, crashFileName)
	last := filepath.Join(dataDir, crashLastFileName)
	previousCrash = nil
	if st, err := os.Stat(cur); err == nil && st.Size() > 0 {
		head := readHead(cur, crashHeadBytes)
		_ = os.Remove(last)
		if err := os.Rename(cur, last); err == nil {
			previousCrash = &CrashInfo{File: last, Time: st.ModTime(), Size: st.Size(), Headline: crashHeadline(head)}
			log.Printf("[warn] the previous run ended in a crash (%s, %d bytes): %s", last, st.Size(), previousCrash.Headline)
		}
	}
	f, err := os.OpenFile(cur, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	// SetCrashOutput keeps its own copy of the descriptor, so ours can be closed right away.
	defer f.Close()
	return debug.SetCrashOutput(f, debug.CrashOptions{})
}

// CloseCrash stops copying crash output into the file and releases the runtime's handle on it.
func CloseCrash() {
	mu.Lock()
	defer mu.Unlock()
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
}

// PreviousCrash reports the crash the last run ended with, if there was one.
func PreviousCrash() (CrashInfo, bool) {
	mu.Lock()
	defer mu.Unlock()
	if previousCrash == nil {
		return CrashInfo{}, false
	}
	return *previousCrash, true
}

func readHead(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf, _ := io.ReadAll(io.LimitReader(f, int64(n)))
	return string(buf)
}

// crashHeadline picks the line that names the failure. The runtime starts its output with it ("panic: ..." or
// "fatal error: ..."), but a line of its own may come first.
func crashHeadline(head string) string {
	first := ""
	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if first == "" {
			first = line
		}
		if strings.HasPrefix(line, "panic:") || strings.HasPrefix(line, "fatal error:") || strings.HasPrefix(line, "runtime:") {
			return clipRunes(line, crashHeadlineMax)
		}
	}
	return clipRunes(first, crashHeadlineMax)
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
