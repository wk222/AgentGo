package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// IsDockerAvailable checks if the Docker daemon is running and reachable.
func IsDockerAvailable(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "docker", "info")
	err := cmd.Run()
	return err == nil
}

// formatVolumeMount normalizes host paths for Docker volume mounting.
func formatVolumeMount(path string) string {
	cleanPath := filepath.Clean(path)
	return strings.ReplaceAll(cleanPath, "\\", "/")
}

// RunInContainer runs a command inside a Docker container with a volume mount.
func RunInContainer(ctx context.Context, image string, hostDir string, containerDir string, cmdArgs []string, env []string) (string, string, int, error) {
	volumeMount := fmt.Sprintf("%s:%s", formatVolumeMount(hostDir), containerDir)

	args := []string{
		"run", "--rm",
		"-v", volumeMount,
		"-w", containerDir,
	}

	for _, e := range env {
		args = append(args, "-e", e)
	}

	args = append(args, image)
	args = append(args, cmdArgs...)

	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			return "", "", 0, err
		}
	}
	return stdout.String(), stderr.String(), exitCode, nil
}
