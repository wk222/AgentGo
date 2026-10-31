package applog

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A desktop (windowsgui) process has no usable stderr. The log file must still get every line:
// with stderr listed first in an io.MultiWriter the first failing writer ended the write, and the
// desktop build kept an empty agentgo.log (so the diagnostics bundle had no log to attach).
func TestTheLogFileGetsEveryLineWhenStderrIsUnusable(t *testing.T) {
	dir := t.TempDir()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	_ = w.Close() // every write to w now fails, like a GUI process without a console
	realStderr := os.Stderr
	t.Cleanup(func() { os.Stderr = realStderr; log.SetOutput(realStderr) })
	os.Stderr = w

	SetQuiet(false)
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	defer Close()
	log.Printf("[test] this line must reach the file")
	log.Printf("[test] and so must the next one")

	data, err := os.ReadFile(filepath.Join(dir, "agentgo.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"logging to", "this line must reach the file", "and so must the next one"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the log file lost %q while stderr was unusable:\n%s", want, text)
		}
	}
}

func TestRotateMovesAnOversizedLogAsideAndReplacesTheOldOne(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "agentgo.log")
	old := log + ".old"

	rotate(log, 100) // no file yet: nothing to do, nothing to fail
	if err := os.WriteFile(log, []byte(strings.Repeat("a", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	rotate(log, 100)
	if _, err := os.Stat(log); err != nil {
		t.Fatalf("a log exactly at the limit must stay: %v", err)
	}

	if err := os.WriteFile(log, []byte(strings.Repeat("b", 101)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("previous generation"), 0o644); err != nil {
		t.Fatal(err)
	}
	rotate(log, 100)
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("the oversized log is still in place: %v", err)
	}
	got, err := os.ReadFile(old)
	if err != nil || len(got) != 101 {
		t.Fatalf("the oversized log did not replace the previous generation: %d bytes, %v", len(got), err)
	}
}

func TestInitStartsAFreshLogWhenTheLastOneIsTooLarge(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agentgo.log"), []byte(strings.Repeat("x", int(MaxLogBytes)+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	SetQuiet(true)
	defer SetQuiet(false)
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	defer Close()
	st, err := os.Stat(filepath.Join(dir, "agentgo.log"))
	if err != nil || st.Size() > 4096 {
		t.Fatalf("the new log should start small: %v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agentgo.log.old")); err != nil {
		t.Fatalf("the big log was not kept as .old: %v", err)
	}
}
