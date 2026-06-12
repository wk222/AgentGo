package sandbox

import (
	"context"
	"strings"
	"testing"
)

func TestIsDockerAvailable(t *testing.T) {
	ctx := context.Background()
	// Should run without panic
	_ = IsDockerAvailable(ctx)
}

func TestRunInContainer(t *testing.T) {
	ctx := context.Background()
	if !IsDockerAvailable(ctx) {
		t.Skip("Docker is not available, skipping container execution test")
	}

	tempDir := t.TempDir()
	stdout, stderr, exitCode, err := RunInContainer(ctx, "alpine", tempDir, "/workspace", []string{"echo", "hello sandbox"}, nil)
	if err != nil {
		t.Fatalf("failed to run container: %v", err)
	}

	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d. Stderr: %q", exitCode, stderr)
	}

	if !strings.Contains(stdout, "hello sandbox") {
		t.Errorf("expected stdout to contain 'hello sandbox', got %q", stdout)
	}
}
