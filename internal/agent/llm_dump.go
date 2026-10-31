package agent

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"agentgo/internal/config"
)

// AGENTGO_DUMP_LLM_DIR=<dir> writes every request body sent to the model as
// <dir>/<seq>.json. It is for prefix-cache forensics (diff two adjacent requests
// and find the first differing byte). The files contain full prompts, so this is
// off by default and must point at a private directory. Headers (API keys) are
// never written.
func dumpLLMDir() string { return config.DumpLLMDir.Get() }

var llmDumpSeq atomic.Uint64

func dumpLLMRequest(raw []byte) {
	dir := dumpLLMDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	name := fmt.Sprintf("%s-%04d.json", time.Now().Format("150405.000"), llmDumpSeq.Add(1))
	_ = os.WriteFile(filepath.Join(dir, name), raw, 0o600)
}

// llmHTTPClient returns the resilient LLM HTTP client (two-stage connect + stream idle
// timeout, pre-output SSE 507 retry, outbound byte calibration, plus shape/dump logging
// when AGENTGO_DEBUG_LLM or AGENTGO_DUMP_LLM_DIR is set).
func llmHTTPClient(timeout time.Duration) *http.Client {
	return newResilientLLMClient(timeout, debugLLMEnabled())
}

// dumpTransport dumps POST bodies, then forwards. Used where the shape logger is
// not installed (the Responses channel).
type dumpTransport struct{ next http.RoundTripper }

func (t dumpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if dumpLLMDir() != "" && req.Body != nil && req.Method == http.MethodPost {
		raw, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err == nil {
			dumpLLMRequest(raw)
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
		req.ContentLength = int64(len(raw))
	}
	return t.next.RoundTrip(req)
}
