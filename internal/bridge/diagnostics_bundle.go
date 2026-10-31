package bridge

import (
	"archive/zip"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentgo/internal/redact"
)

// A diagnostics bundle is one zip a user can hand to whoever supports AgentGo: what runs, with which settings
// (never the keys), what the log said last. It carries no conversation, memory or workspace content.
const (
	diagnosticsPrefix = "agentgo-diagnostics-"
	diagnosticsKeep   = 3         // bundles kept; the next export removes older ones
	logTailBytes      = 768 << 10 // how much of the end of agentgo.log goes into the bundle
)

// diagFile is one entry of the bundle.
type diagFile struct {
	Name string
	Data []byte
}

// redactSecrets hides API keys, bearer tokens, key=value credentials, URL tokens and the Windows account name.
// It is applied to everything that goes into a bundle, on top of each section leaving the secrets out already.
func redactSecrets(s string) string { return redact.Secrets(s) }

// sanitizeURL keeps scheme, host and path of a service address and drops credentials, query and fragment.
func sanitizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return redactSecrets(raw)
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

// tailFile returns the last max bytes of a text file, starting at a line boundary. A short file is returned whole.
func tailFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	skipped := st.Size() > max
	if skipped {
		if _, err := f.Seek(st.Size()-max, io.SeekStart); err != nil {
			return nil, err
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if skipped {
		if i := strings.IndexByte(string(data), '\n'); i >= 0 {
			data = data[i+1:]
		}
		data = append([]byte("... (earlier lines left out)\n"), data...)
	}
	return data, nil
}

var diagMu sync.Mutex // one export at a time: two clicks must not write the same file

// writeDiagnosticsBundle zips files into dir/name (written to a temporary name first, so a download never
// sees a half-written zip), then removes bundles beyond diagnosticsKeep. Every entry passes through
// redactSecrets.
func writeDiagnosticsBundle(dir, name string, files []diagFile, now time.Time) (string, int64, error) {
	diagMu.Lock()
	defer diagMu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	final := filepath.Join(dir, name)
	tmp := final + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return "", 0, err
	}
	zw := zip.NewWriter(out)
	for _, file := range files {
		h := &zip.FileHeader{Name: filepath.ToSlash(file.Name), Method: zip.Deflate, Modified: now}
		w, err := zw.CreateHeader(h)
		if err == nil {
			_, err = io.WriteString(w, redactSecrets(string(file.Data)))
		}
		if err != nil {
			_ = zw.Close()
			_ = out.Close()
			_ = os.Remove(tmp)
			return "", 0, err
		}
	}
	if err := zw.Close(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return "", 0, err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", 0, err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", 0, err
	}
	pruneDiagnostics(dir, diagnosticsKeep)
	st, err := os.Stat(final)
	if err != nil {
		return "", 0, err
	}
	return final, st.Size(), nil
}

// pruneDiagnostics removes all but the newest keep bundles (their names carry the time) and any leftover
// half-written file from a crash.
func pruneDiagnostics(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var bundles []string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, diagnosticsPrefix) {
			continue
		}
		switch {
		case strings.HasSuffix(n, ".zip.tmp"):
			_ = os.Remove(filepath.Join(dir, n))
		case strings.HasSuffix(n, ".zip"):
			bundles = append(bundles, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(bundles)))
	for i := keep; i < len(bundles); i++ {
		_ = os.Remove(filepath.Join(dir, bundles[i]))
	}
}

// diagnosticsName is the file name of the bundle made at now.
func diagnosticsName(now time.Time) string {
	return fmt.Sprintf("%s%s.zip", diagnosticsPrefix, now.Format("20060102-150405"))
}
