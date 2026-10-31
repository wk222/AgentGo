package apps

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreviewServerPoolIsolationAndLRUEviction(t *testing.T) {
	pool := NewPreviewServerPool(2)
	defer pool.Close()

	makeBundle := func(name string) string {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><head></head><body><h1>"+name+"</h1></body></html>"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('"+name+"')"), 0o644)
		return dir
	}

	dir1 := makeBundle("app1")
	dir2 := makeBundle("app2")
	dir3 := makeBundle("app3")

	lease1, err := pool.AcquirePanelPreview(dir1, PreviewSessionBinding{
		AppName:   "app1",
		SessionID: "ias_1",
		Nonce:     "ian_1",
	})
	if err != nil {
		t.Fatalf("AcquirePanelPreview app1: %v", err)
	}
	lease2, err := pool.AcquirePanelPreview(dir2, PreviewSessionBinding{
		AppName:   "app2",
		SessionID: "ias_2",
		Nonce:     "ian_2",
	})
	if err != nil {
		t.Fatalf("AcquirePanelPreview app2: %v", err)
	}

	// 1. Each preview must get its own distinct loopback port (for localStorage isolation)
	if lease1.Port == lease2.Port {
		t.Fatalf("expected distinct ports for app1 and app2, both got %d", lease1.Port)
	}

	// 2. EntryURL serves index.html with injected window.agentGo helper
	resp, err := http.Get(lease1.EntryURL)
	if err != nil {
		t.Fatalf("GET lease1 EntryURL: %v", err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	body := string(bodyBytes)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "window.agentGo") || !strings.Contains(body, `"ias_1"`) {
		t.Fatalf("unexpected entry HTML (status %d): %s", resp.StatusCode, body)
	}

	// 3. Port scan without valid capability handle returns 404
	badResp, err := http.Get(fmt.Sprintf("%s/wrong_handle/index.html", lease1.Origin))
	if err != nil {
		t.Fatalf("GET wrong handle: %v", err)
	}
	_ = badResp.Body.Close()
	if badResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for wrong handle, got %d", badResp.StatusCode)
	}

	// 4. Path traversal attempt returns 404
	travResp, err := http.Get(fmt.Sprintf("%s/%s/..%%2f..%%2fsecret.txt", lease1.Origin, lease1.Handle))
	if err != nil {
		t.Fatalf("GET traversal: %v", err)
	}
	_ = travResp.Body.Close()
	if travResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for path traversal, got %d", travResp.StatusCode)
	}

	// 5. Acquiring a 3rd preview when maxConcurrent=2 evicts the oldest (lease1)
	lease3, err := pool.AcquirePanelPreview(dir3, PreviewSessionBinding{
		AppName:   "app3",
		SessionID: "ias_3",
		Nonce:     "ian_3",
	})
	if err != nil {
		t.Fatalf("AcquirePanelPreview app3: %v", err)
	}
	if pool.ActiveCount() != 2 {
		t.Fatalf("expected active count 2 after eviction, got %d", pool.ActiveCount())
	}

	// lease3 works, lease2 works
	r3, err := http.Get(lease3.EntryURL)
	if err != nil {
		t.Fatalf("GET lease3: %v", err)
	}
	_ = r3.Body.Close()
	if r3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on lease3, got %d", r3.StatusCode)
	}

	// 6. External preview handle expires after TTL
	extLease, err := pool.IssueExternalPreview(dir2, PreviewSessionBinding{AppName: "app2"}, 60*time.Millisecond)
	if err != nil {
		t.Fatalf("IssueExternalPreview: %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	if pool.Revoke(extLease.Handle) {
		t.Fatalf("expected external preview handle to already be revoked after TTL")
	}
}
