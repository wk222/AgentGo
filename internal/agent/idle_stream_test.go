package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type delayedReadBody struct {
	release chan struct{}
	done    chan struct{}
	started chan struct{}
	once    sync.Once
}

func (b *delayedReadBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	n := copy(p, []byte("LATE"))
	close(b.done)
	return n, io.EOF
}
func (b *delayedReadBody) Close() error { return nil }

func TestIdleReadNeverWritesReturnedCallerBuffer(t *testing.T) {
	raw := &delayedReadBody{release: make(chan struct{}), done: make(chan struct{}), started: make(chan struct{})}
	body := newIdleStreamBody(context.Background(), raw, 20*time.Millisecond)
	p := []byte("SAFE")
	if n, err := body.Read(p); n != 0 || !errors.Is(err, ErrLLMStreamIdleTimeout) {
		t.Fatal(n, err)
	}
	close(raw.release)
	<-raw.done
	if string(p) != "SAFE" {
		t.Fatal("late provider write corrupted caller memory", string(p))
	}
	if _, err := body.Read(p); !errors.Is(err, ErrLLMStreamIdleTimeout) {
		t.Fatal("timeout was not terminal", err)
	}
}

func TestSSEInspectionIsBoundedByIdleTimeoutAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "cancel"}[cancelled], func(t *testing.T) {
			pr, pw := io.Pipe()
			defer pw.Close()
			tr := &llmResilienceTransport{streamIdleTimeout: 25 * time.Millisecond, max507Retries: 2, next: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: pr}, nil
			})}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				cancel()
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://test.invalid/v1/chat/completions", strings.NewReader("{\"model\":\"test\"}"))
			ready := make(chan error, 1)
			go func() {
				resp, err := tr.RoundTrip(req)
				if resp != nil {
					resp.Body.Close()
				}
				ready <- err
			}()
			select {
			case err := <-ready:
				want := ErrLLMStreamIdleTimeout
				if cancelled {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatal("unprotected pre-output read", err)
				}
			case <-time.After(time.Second):
				pw.Close()
				t.Fatal("SSE inspection hung before timeout protection")
			}
		})
	}
}

func TestResponsesFirstEventIsDeliveredWithoutWaitingForCompletion(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	frame := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"first\"}\n\n"
	tr := &llmResilienceTransport{streamIdleTimeout: time.Second, max507Retries: 3, next: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: pr}, nil
	})}
	req, _ := http.NewRequest(http.MethodPost, "http://test.invalid/v1/responses", strings.NewReader("{\"model\":\"test\"}"))
	ready := make(chan *http.Response, 1)
	go func() { resp, _ := tr.RoundTrip(req); ready <- resp }()
	go func() { io.WriteString(pw, frame) }()
	select {
	case resp := <-ready:
		if resp == nil {
			t.Fatal("missing response")
		}
		defer resp.Body.Close()
		got := make([]byte, len(frame))
		if _, err := io.ReadFull(resp.Body, got); err != nil || !bytes.Equal(got, []byte(frame)) {
			t.Fatal(string(got), err)
		}
	case <-time.After(500 * time.Millisecond):
		pw.Close()
		t.Fatal("Responses output was buffered until a later event")
	}
}
