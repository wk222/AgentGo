package acp

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestServer_ZedStyleTranscript replays the exact method names Zed (agent-client-protocol v1)
// sends. When ACP_DUMP is set the raw server output is written there so an external
// schema checker (Rust agent-client-protocol-schema) can validate it.
func TestServer_ZedStyleTranscript(t *testing.T) {
	in := bytes.NewBufferString(
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":true,"writeTextFile":true},"terminal":true},"clientInfo":{"name":"zed","version":"0.999"}}}` + "\n" +
			`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"C:\\proj","mcpServers":[]}}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"agentgo-acp-1","prompt":[{"type":"text","text":"hello"}]}}` + "\n" +
			`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"agentgo-acp-1"}}` + "\n",
	)
	var out bytes.Buffer
	srv := NewServer(&dummyHandlerNilNew{}, in, &out)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, srv.Serve(ctx))
	time.Sleep(100 * time.Millisecond)

	srv.mu.Lock()
	raw := out.String()
	srv.mu.Unlock()

	// A cancel notification must never be answered.
	for _, l := range strings.Split(strings.TrimSpace(raw), "\n") {
		require.NotContains(t, l, `"id":null`)
		require.NotContains(t, l, `"sessionUpdate":"thought"`)
	}
	require.Contains(t, raw, `"method":"session/update"`)

	if p := os.Getenv("ACP_DUMP"); p != "" {
		require.NoError(t, os.WriteFile(p, []byte(raw), 0o644))
	}
}

// dummyHandlerNilNew defers session creation to the server defaults.
type dummyHandlerNilNew struct{ dummyHandler }

func (d *dummyHandlerNilNew) NewSession(params NewSessionParams) (*NewSessionResult, error) {
	return nil, nil
}
