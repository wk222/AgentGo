package applog

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

var (
	mu    sync.Mutex
	path  string
	quiet bool
	file  *os.File // the open log file, so a second Init (or Close) can release it
)

// SetQuiet keeps process logs out of stderr (they still go to the log file).
// Terminal clients call it before Init so log lines do not interleave with the
// conversation.
func SetQuiet(q bool) {
	mu.Lock()
	quiet = q
	mu.Unlock()
}

// MaxLogBytes is how large agentgo.log may be when the app starts. A bigger file is moved to
// agentgo.log.old (replacing the previous one), so the log never fills a work PC's disk: at most about two
// files of this size are kept.
const MaxLogBytes int64 = 16 << 20

// Init appends process logs to dataDir/agentgo.log (and stderr unless quiet).
func Init(dataDir string) error {
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	path = filepath.Join(dataDir, "agentgo.log")
	rotate(path, MaxLogBytes)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if quiet {
		log.SetOutput(f)
	} else {
		log.SetOutput(fileFirst{file: f, stderr: os.Stderr})
	}
	if file != nil {
		_ = file.Close()
	}
	file = f
	log.Printf("[agentgo] logging to %s", path)
	return nil
}

// fileFirst writes every log line to the log file and, best effort, to stderr. A desktop (windowsgui)
// process has no usable stderr, and io.MultiWriter ends the write at the first writer that fails, so
// listing stderr ahead of the file used to leave the desktop build with an empty agentgo.log.
type fileFirst struct {
	file   io.Writer
	stderr io.Writer
}

func (w fileFirst) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	_, _ = w.stderr.Write(p)
	return n, err
}

// Close stops logging to the file (process logs go back to stderr) and releases it.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	log.SetOutput(os.Stderr)
	if file != nil {
		_ = file.Close()
		file = nil
	}
}

// rotate moves an oversized log aside. Failing to do so is not fatal: the log just keeps growing.
func rotate(path string, max int64) {
	st, err := os.Stat(path)
	if err != nil || st.Size() <= max {
		return
	}
	old := path + ".old"
	_ = os.Remove(old)
	_ = os.Rename(path, old)
}

// Path returns the active log file path (empty if Init not called).
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

func Session(format string, args ...any) {
	log.Printf("[session] "+format, args...)
}

func IPC(method string, format string, args ...any) {
	log.Printf("[ipc] %s "+format, append([]any{method}, args...)...)
}

func Stream(format string, args ...any) {
	log.Printf("[stream] "+format, args...)
}

func Warn(format string, args ...any) {
	log.Printf("[warn] "+format, args...)
}

func A2UI(format string, args ...any) {
	log.Printf("[a2ui] "+format, args...)
}

func UI(format string, args ...any) {
	log.Printf("[ui] "+format, args...)
}

func FormatCounts(ids map[string]int) string {
	if len(ids) == 0 {
		return "none"
	}
	out := ""
	for k, n := range ids {
		if out != "" {
			out += ", "
		}
		out += fmt.Sprintf("%s=%d", k, n)
	}
	return out
}
