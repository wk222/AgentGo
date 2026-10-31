//go:build linux

package sandbox

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// run 在沙箱内执行 line（测试二进制本身充当垫片，见 init）。
func runConfined(t *testing.T, dir, line string, allowNet bool) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd, err := Confine(ctx, line, dir, nil, allowNet)
	if err != nil {
		t.Fatalf("confine: %v", err)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	return out.String(), err
}

func TestLandlockWriteBoundary(t *testing.T) {
	if !Available() {
		t.Skip("Landlock not available on this kernel")
	}
	ws := t.TempDir()
	outside, err := os.MkdirTemp("/var/tmp", "agentgo-sbx-")
	if err != nil {
		t.Skipf("no /var/tmp: %v", err)
	}
	defer os.RemoveAll(outside)

	if out, err := runConfined(t, ws, "echo ok > inside.txt", false); err != nil {
		t.Fatalf("write inside workspace must work: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "inside.txt")); string(bytes.TrimSpace(b)) != "ok" {
		t.Fatalf("inside.txt = %q", b)
	}

	target := filepath.Join(outside, "evil.txt")
	if out, err := runConfined(t, ws, "echo x > "+target, false); err == nil {
		t.Fatalf("write outside the roots must fail, output: %s", out)
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("file was created outside the sandbox")
	}
}

func TestLandlockReadBoundary(t *testing.T) {
	if !Available() {
		t.Skip("Landlock not available on this kernel")
	}
	outside, err := os.MkdirTemp("/var/tmp", "agentgo-sbx-")
	if err != nil {
		t.Skipf("no /var/tmp: %v", err)
	}
	defer os.RemoveAll(outside)
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("hunter2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runConfined(t, t.TempDir(), "cat "+secret, false); err == nil {
		t.Fatalf("reading outside the read roots must fail, output: %s", out)
	}
}

func TestShimDoesNotLeakPolicyEnv(t *testing.T) {
	if !Available() {
		t.Skip("Landlock not available on this kernel")
	}
	out, err := runConfined(t, t.TempDir(), "env", false)
	if err != nil {
		t.Fatalf("env: %v\n%s", err, out)
	}
	if bytes.Contains([]byte(out), []byte(policyEnv)) {
		t.Fatalf("policy env leaked into the confined command:\n%s", out)
	}
}
