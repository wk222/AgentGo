package agent

import (
	"context"
	"io"
	"sync"
	"time"
)

// idleStreamBody owns read buffers so a timed-out read can never modify memory
// already returned to its caller. Cancellation and Close are terminal.
type idleStreamBody struct {
	inner    io.ReadCloser
	ctx      context.Context
	timeout  time.Duration
	readMu   sync.Mutex
	mu       sync.Mutex
	terminal error
	closed   chan struct{}
	once     sync.Once
	closeErr error
}

func newIdleStreamBody(ctx context.Context, inner io.ReadCloser, timeout time.Duration) *idleStreamBody {
	return &idleStreamBody{inner: inner, ctx: ctx, timeout: timeout, closed: make(chan struct{})}
}

func (b *idleStreamBody) closeInner() error {
	b.once.Do(func() { close(b.closed); b.closeErr = b.inner.Close() })
	return b.closeErr
}

func (b *idleStreamBody) Close() error {
	b.mu.Lock()
	if b.terminal == nil {
		b.terminal = io.ErrClosedPipe
	}
	b.mu.Unlock()
	return b.closeInner()
}

func (b *idleStreamBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b.readMu.Lock()
	defer b.readMu.Unlock()
	b.mu.Lock()
	terminal := b.terminal
	b.mu.Unlock()
	if terminal != nil {
		return 0, terminal
	}
	if err := b.ctx.Err(); err != nil {
		b.stop(err)
		return 0, err
	}
	// A misbehaving provider may finish Read after Close. At most one bounded,
	// privately owned buffer survives a timeout; the caller's p is never shared.
	data := make([]byte, min(len(p), 64*1024))
	type result struct {
		n   int
		err error
	}
	ready := make(chan result, 1)
	go func() { n, err := b.inner.Read(data); ready <- result{n, err} }()
	timer := time.NewTimer(b.timeout)
	defer timer.Stop()
	select {
	case r := <-ready:
		if r.err != nil {
			b.mu.Lock()
			b.terminal = r.err
			b.mu.Unlock()
		}
		copy(p, data[:r.n])
		return r.n, r.err
	case <-timer.C:
		b.stop(ErrLLMStreamIdleTimeout)
		return 0, ErrLLMStreamIdleTimeout
	case <-b.ctx.Done():
		err := b.ctx.Err()
		b.stop(err)
		return 0, err
	case <-b.closed:
		b.mu.Lock()
		err := b.terminal
		b.mu.Unlock()
		if err == nil {
			err = io.ErrClosedPipe
		}
		return 0, err
	}
}

func (b *idleStreamBody) stop(err error) {
	b.mu.Lock()
	if b.terminal == nil {
		b.terminal = err
	}
	b.mu.Unlock()
	_ = b.closeInner()
}
