package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
)

// diskReductionBackend implements reduction.Backend (Write-only) without eino-ext/local.
type diskReductionBackend struct {
	root string
}

func newDiskReductionBackend(root string) reduction.Backend {
	return &diskReductionBackend{root: filepath.Clean(root)}
}

func (b *diskReductionBackend) Write(_ context.Context, req *filesystem.WriteRequest) error {
	if req == nil {
		return os.ErrInvalid
	}
	p := req.FilePath
	if !filepath.IsAbs(p) {
		p = filepath.Join(b.root, filepath.Clean(strings.TrimPrefix(p, "/")))
	}
	p = filepath.Clean(p)
	if !strings.HasPrefix(p, b.root+string(os.PathSeparator)) && p != b.root {
		return os.ErrPermission
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(req.Content), 0o644)
}

// ReductionTruncationEnabled enables file offload truncation (Linux/CI default; Windows needs opt-in).
func ReductionTruncationEnabled() bool {
	if boolEnv("AGENTGO_REDUCTION_TRUNC") {
		return true
	}
	if strings.TrimSpace(os.Getenv("AGENTGO_REDUCTION_BACKEND")) != "" {
		return true
	}
	return runtime.GOOS != "windows"
}

func reductionOffloadRoot(dataDir string) string {
	if v := strings.TrimSpace(os.Getenv("AGENTGO_REDUCTION_BACKEND")); v != "" {
		return v
	}
	return filepath.Join(dataDir, "tool_offload")
}

// BuildReductionConfig returns reduction middleware config for Message / Agentic paths.
func BuildReductionConfig(dataDir string) *reduction.Config {
	cfg := &reduction.Config{
		SkipTruncation:    true,
		MaxTokensForClear: 120_000,
		MaxLengthForTrunc: 30_000,
		ReadFileToolName:  "read_file",
	}
	if !ReductionTruncationEnabled() {
		return cfg
	}
	root := reductionOffloadRoot(dataDir)
	_ = os.MkdirAll(root, 0o755)
	cfg.SkipTruncation = false
	cfg.Backend = newDiskReductionBackend(root)
	cfg.RootDir = root
	return cfg
}
