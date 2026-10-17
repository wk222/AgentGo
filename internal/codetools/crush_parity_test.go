//go:build crushparity

package codetools

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"agentgo/internal/codelsp"
	"agentgo/internal/coderuntime"
	ct "agentgo/third_party/crush/pkg/codetools"
)

// Parity against the vendored Crush tools (plan-crush-digestion D2/D3). The
// native tools replaced Crush's, so production no longer links Crush for them;
// this file brings the Crush implementation back as the reference:
//
//	go test -tags crushparity ./internal/codetools
func init() {
	crushBoxFactory = func(t *testing.T, dir string) coderuntime.Toolbox {
		tb, err := ct.New(ct.Options{WorkDir: dir, DataDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(tb.Close)
		return Wrap(tb)
	}
}

func waitGoplsReady(t *testing.T, box coderuntime.Toolbox, file string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		box.StartLSP(context.Background(), file)
		if st, ok := box.LSPStates()["gopls"]; ok && st.State == int(codelsp.StateReady) {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("gopls never became ready")
}

func TestNativeLSPParityWithCrush(t *testing.T) {
	needGopls(t)

	crushDir := t.TempDir()
	seedGoModule(t, crushDir)
	// Two Crush quirks the baseline has to be shielded from; the native side
	// gets neither help. Its symbol tools grep from the process's cwd, not the
	// workspace; and a call that arrives while a server is still starting (the
	// toolbox primes servers in the background) queries it before initialize.
	t.Chdir(crushDir)
	crushBox := crushBoxFactory(t, crushDir)
	waitGoplsReady(t, crushBox, filepath.Join(crushDir, "a.go"))
	want := lspTranscript(t, crushBox, crushDir)

	nativeDir := t.TempDir()
	seedGoModule(t, nativeDir)
	got := lspTranscript(t, newNativeLSP(t, nativeDir), nativeDir)

	all := strings.Join(want, "\n")
	for _, marker := range lspMarkers {
		if !strings.Contains(all, marker) {
			t.Errorf("crush transcript never produced %q:\n%s", marker, all)
		}
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d differs\n--- crush ---\n%s\n--- native ---\n%s", i, want[i], got[i])
		}
	}
}

// The production assembly must expose exactly the tool names the full Crush
// toolbox does, so nothing the model or the governance traits refer to goes
// missing.
func TestNativeAssemblyMatchesCrushToolSet(t *testing.T) {
	names := func(tb coderuntime.Toolbox) string {
		var n []string
		for _, tl := range tb.Tools() {
			n = append(n, tl.Name)
		}
		sort.Strings(n)
		return strings.Join(n, ",")
	}
	dir := t.TempDir()
	if got, want := names(nativeBox(t, dir)), names(crushBoxFactory(t, dir)); got != want {
		t.Fatalf("tool set changed\n got: %s\nwant: %s", got, want)
	}
}
