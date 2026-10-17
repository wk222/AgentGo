package tui

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// A real tea.Program over pipes: type a message, see the answer on screen,
// leave with Ctrl+D.
func TestProgramEndToEnd(t *testing.T) {
	be := &fakeBE{run: answerRun("pong")}
	pr, pw := io.Pipe()
	defer pw.Close()
	var out syncBuf
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- runHeadless(ctx, New(be, Options{}), pr, &out) }()

	time.Sleep(300 * time.Millisecond) // let the program start reading
	_, _ = pw.Write([]byte("ping\r"))
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(ansi.ReplaceAllString(out.String(), ""), "pong") {
		if time.Now().After(deadline) {
			t.Fatalf("answer never reached the screen:\n%s", out.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, _ = pw.Write([]byte("\x04"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ctrl+D did not end the program")
	}
}
