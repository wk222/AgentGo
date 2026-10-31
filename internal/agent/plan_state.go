package agent

import (
	"agentgo/internal/sessions"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func planTaskDirectory(ctx context.Context, workspace, dataDir string) string {
	base := filepath.Join(workspace, ".tasks")
	if strings.TrimSpace(workspace) == "" {
		base = filepath.Join(dataDir, "tasks")
	}
	if absolute, err := filepath.Abs(base); err == nil {
		base = absolute
	}
	sid := strings.TrimPrefix(SessionIDFromContext(ctx), "agentic:")
	if sid == "" {
		sid = "desktop"
	}
	digest := sha256.Sum256([]byte(sid))
	return filepath.Join(base, fmt.Sprintf("session-%x", digest[:12]))
}

func loadPlanTaskSnapshot(ctx context.Context, dir string) ([]sessions.PlanTaskSnapshot, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		a, _ := strconv.ParseUint(strings.TrimSuffix(entries[i].Name(), ".json"), 10, 64)
		b, _ := strconv.ParseUint(strings.TrimSuffix(entries[j].Name(), ".json"), 10, 64)
		return a > b
	})
	var out []sessions.PlanTaskSnapshot
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.IsDir() || e.Type()&os.ModeSymlink != 0 || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		if len(out) >= 128 {
			break
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(f, 65537))
		f.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(data) > 65536 {
			return nil, fmt.Errorf("plan task record too large")
		}
		var t sessions.PlanTaskSnapshot
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, fmt.Errorf("read plan task %s: %w", e.Name(), err)
		}
		if t.ID == "" || len(t.ID) > 32 || len(t.BlockedBy) > 128 {
			return nil, fmt.Errorf("invalid plan task %s", e.Name())
		}
		if t.Status != "deleted" {
			out = append(out, t)
		}
	}
	return out, nil
}
