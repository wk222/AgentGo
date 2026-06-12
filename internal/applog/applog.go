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
	mu   sync.Mutex
	path string
)

// Init appends process logs to dataDir/agentgo.log (and stderr).
func Init(dataDir string) error {
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	path = filepath.Join(dataDir, "agentgo.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.Printf("[agentgo] logging to %s", path)
	return nil
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
