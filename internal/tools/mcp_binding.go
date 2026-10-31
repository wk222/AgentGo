package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"sync"

	"agentgo/internal/governance"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// One connection generation owns its tools and declarations. Retirement blocks
// captured old tool objects; calls already in progress finish on their old peer.
type mcpBinding struct {
	mu       sync.Mutex
	retired  bool
	calls    int
	closer   io.Closer
	dispose  []func()
	closed   chan struct{}
	onClosed func()
	// life ends when the connection is forcibly retired. A pending request on a
	// closed transport is not woken by the transport itself, so calls watch it.
	life  context.Context
	abort context.CancelFunc
}

func (b *mcpBinding) acquire() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.retired {
		return false
	}
	b.calls++
	return true
}
func (b *mcpBinding) release() {
	b.mu.Lock()
	b.calls--
	var closer io.Closer
	if b.retired && b.calls == 0 {
		closer, b.closer = b.closer, nil
	}
	b.mu.Unlock()
	if closer != nil {
		b.finishClose(closer)
	}
}

func (b *mcpBinding) finishClose(closer io.Closer) {
	_ = closer.Close()
	b.abort()
	b.onClosed()
	close(b.closed)
}

// Called while the registry is locked, but neither closes I/O nor runs hooks.
func (b *mcpBinding) retire(force bool) func() {
	b.mu.Lock()
	b.retired = true
	var closer io.Closer
	if force || b.calls == 0 {
		closer, b.closer = b.closer, nil
	}
	dispose := b.dispose
	b.dispose = nil
	b.mu.Unlock()
	return func() {
		if force {
			b.abort() // before Close: unblock calls waiting on the dying peer
		}
		for i := len(dispose) - 1; i >= 0; i-- {
			dispose[i]()
		}
		if closer != nil {
			b.finishClose(closer)
		}
	}
}

type boundMCPTool struct {
	inner      tool.InvokableTool
	info       schema.ToolInfo
	binding    *mcpBinding
	definition string
}

func (t *boundMCPTool) Info(context.Context) (*schema.ToolInfo, error) {
	info := t.info
	info.Extra = make(map[string]any, len(t.info.Extra))
	for k, v := range t.info.Extra {
		info.Extra[k] = v
	}
	return &info, nil
}
func (t *boundMCPTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !governance.ToolDefinitionMatches(ctx, t.definition) {
		return "", fmt.Errorf("mcp: tool definition changed; request the current tool again")
	}
	if !t.binding.acquire() {
		return "", fmt.Errorf("mcp: tool connection retired; request the current tool again")
	}
	defer t.binding.release()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := context.AfterFunc(t.binding.life, func() { cancel(fmt.Errorf("mcp: connection closed")) })
	defer stop()
	return t.inner.InvokableRun(ctx, args, opts...)
}

func mcpModelName(server, remote string) string {
	name := server + "_" + remote
	if ValidToolName(name) {
		return name
	}
	var safe strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-' {
			safe.WriteRune(r)
		} else {
			safe.WriteByte('_')
		}
	}
	prefix := safe.String()
	if len(prefix) > 52 {
		prefix = prefix[:52]
	}
	h := sha256.Sum256([]byte(server + "\x00" + remote))
	return prefix + "_" + hex.EncodeToString(h[:])[:10]
}
