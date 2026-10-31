package bridge

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentgo/internal/applog"
	"agentgo/internal/memory"
)

func TestRedactSecretsHidesCredentialsAndLeavesOrdinaryText(t *testing.T) {
	hidden := []struct{ in, want string }{
		{`Authorization: Bearer abcdefgh12345678`, `Authorization: Bearer ***`},
		{`{"api_key": "sk-abcdefghijklmnop", "model": "m"}`, `{"api_key": "***", "model": "m"}`},
		{`AZURE_OPENAI_API_KEY=abcd1234efgh5678`, `AZURE_OPENAI_API_KEY=***`},
		{`open http://127.0.0.1:8788/?t=0123456789abcdef now`, `open http://127.0.0.1:8788/?t=*** now`},
		{`key sk-live_1234567890abcdef used`, `key sk-*** used`},
		{`C:\Users\user\Documents\GitHub\agentgo`, `C:\Users\<user>\Documents\GitHub\agentgo`},
		{`"data_dir": "C:\\Users\\张三\\AppData\\Local"`, `"data_dir": "C:\\Users\\<user>\\AppData\\Local"`},
	}
	for _, c := range hidden {
		if got := redactSecrets(c.in); got != c.want {
			t.Errorf("redact(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
	}
	untouched := []string{
		`task-runner-config disk-usage-report`,
		`{"name": "AGENTGO_API_KEY", "secret": true, "value": "已配置"}`,
		`max_tokens: 4096 prompt_tokens=1200 "cached_tokens": 12`,
		`token budget is 4000, password policy documented`,
	}
	for _, s := range untouched {
		if got := redactSecrets(s); got != s {
			t.Errorf("redact changed ordinary text:\n in  %q\n out %q", s, got)
		}
	}
}

func TestSanitizeURLDropsCredentialsQueryAndFragment(t *testing.T) {
	if got := sanitizeURL(" https://user:pw@api.example.com/v1?key=abc#frag "); got != "https://api.example.com/v1" {
		t.Fatalf("got %q", got)
	}
	if sanitizeURL("") != "" {
		t.Fatal("empty stays empty")
	}
	if got := sanitizeURL("api_key=abcdefghijkl"); strings.Contains(got, "abcdefghijkl") {
		t.Fatalf("something that is not a URL must still be redacted: %q", got)
	}
}

func TestTailFileStartsAtALineBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentgo.log")
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "line %03d xxxxxxxxxx\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	whole, err := tailFile(path, 1<<20)
	if err != nil || string(whole) != b.String() {
		t.Fatalf("a short file must come back whole: %v", err)
	}
	tail, err := tailFile(path, 200)
	if err != nil {
		t.Fatal(err)
	}
	text := string(tail)
	if !strings.HasPrefix(text, "... (earlier lines left out)\n") || !strings.HasSuffix(text, "line 099 xxxxxxxxxx\n") {
		t.Fatalf("tail: %q", text)
	}
	for _, line := range strings.Split(strings.TrimSpace(strings.TrimPrefix(text, "... (earlier lines left out)\n")), "\n") {
		if !strings.HasPrefix(line, "line ") || len(line) != 19 {
			t.Fatalf("a partial line leaked into the tail: %q", line)
		}
	}
	if _, err := tailFile(filepath.Join(t.TempDir(), "missing.log"), 10); err == nil {
		t.Fatal("a missing log must be an error the caller can report")
	}
}

func readZip(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = string(data)
	}
	return out
}

func TestWriteDiagnosticsBundleRedactsEveryEntryAndKeepsOnlyTheNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.Local)
	var last string
	for i := 0; i < 5; i++ {
		now := base.Add(time.Duration(i) * time.Second)
		files := []diagFile{
			{Name: "a.json", Data: []byte(`{"api_key": "sk-abcdefghijklmnop"}`)},
			{Name: `logs\b.txt`, Data: []byte("Authorization: Bearer abcdefgh12345678\n")},
		}
		path, size, err := writeDiagnosticsBundle(dir, diagnosticsName(now), files, now)
		if err != nil || size <= 0 {
			t.Fatalf("write %d: %v (%d bytes)", i, err, size)
		}
		last = path
	}
	got := readZip(t, last)
	if len(got) != 2 || strings.Contains(got["a.json"], "sk-abcdefghijklmnop") || strings.Contains(got["logs/b.txt"], "abcdefgh12345678") {
		t.Fatalf("zip content: %v", got)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != diagnosticsKeep {
		t.Fatalf("expected the %d newest bundles, found %v", diagnosticsKeep, names)
	}
	for _, n := range names {
		if strings.HasSuffix(n, ".tmp") {
			t.Fatalf("temporary file left behind: %v", names)
		}
	}
	if !strings.Contains(strings.Join(names, " "), "20261027-120004") || strings.Contains(strings.Join(names, " "), "20261027-120000") {
		t.Fatalf("the oldest bundle must be the one removed: %v", names)
	}
}

func diagnosticsService(t *testing.T) (*AppService, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DB().Close() })
	if err := store.Ingest(t.Context(), memory.Record{ID: "m1", Scope: "global", Status: "active", Modality: "fact", Importance: 1, Content: "SECRET-BUSINESS-CONTENT-DO-NOT-EXPORT"}); err != nil {
		t.Fatal(err)
	}
	rt := &Runtime{
		dataDir:   dir,
		workspace: filepath.Join(dir, "ws"),
		memStore:  store,
		llm: LLMConfig{
			APIBase: "https://user:pw@gw.example.com/v1?key=sk-querysecret123456", APIKey: "sk-thisisthesecretkey123456",
			Model: "big-model", FallbackModel: "small-model",
		},
	}
	return &AppService{rt: rt}, dir
}

func TestExportDiagnosticsProducesAZipWithoutKeysOrContent(t *testing.T) {
	svc, dir := diagnosticsService(t)
	applog.SetQuiet(true)
	if err := applog.Init(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { applog.Close(); applog.SetQuiet(false) }()
	log.Printf("calling model with api_key=sk-thisisthesecretkey123456 from C:\\Users\\user\\x")

	res := svc.ExportDiagnostics()
	if res["success"] != true {
		t.Fatalf("export failed: %v", res)
	}
	name := res["name"].(string)
	if res["url"] != "/attachments/"+name || !strings.HasPrefix(name, diagnosticsPrefix) || res["size"].(int64) <= 0 {
		t.Fatalf("answer: %v", res)
	}
	if want := filepath.Join(dir, "attachments", name); res["path"] != want {
		t.Fatalf("path %v, want %v", res["path"], want)
	}

	got := readZip(t, res["path"].(string))
	for _, need := range []string{"README.txt", "environment.json", "llm.json", "features.json", "config.json", "plugins.json", "tools.json", "database.json", "logs/agentgo.log.tail.txt"} {
		if _, ok := got[need]; !ok {
			t.Errorf("%s is missing; have %v", need, keysOf(got))
		}
	}
	for name, content := range got {
		for _, forbidden := range []string{"sk-thisisthesecretkey123456", "sk-querysecret123456", "user:pw", "SECRET-BUSINESS-CONTENT", `\Users\user\`, `\\Users\\user\\`} {
			if strings.Contains(content, forbidden) {
				t.Errorf("%s leaks %q", name, forbidden)
			}
		}
	}
	var llm map[string]any
	if err := json.Unmarshal([]byte(got["llm.json"]), &llm); err != nil {
		t.Fatalf("llm.json: %v", err)
	}
	if llm["api_base"] != "https://gw.example.com/v1" || llm["api_key_set"] != true || llm["model"] != "big-model" {
		t.Fatalf("llm.json: %v", llm)
	}
	var db struct {
		TableRows  map[string]any `json:"table_rows"`
		QuickCheck string         `json:"quick_check"`
		Memories   []struct {
			Count int64 `json:"count"`
		} `json:"memories_by_status"`
	}
	if err := json.Unmarshal([]byte(got["database.json"]), &db); err != nil {
		t.Fatalf("database.json: %v", err)
	}
	if db.TableRows["memories"] != float64(1) || db.QuickCheck != "ok" || len(db.Memories) != 1 || db.Memories[0].Count != 1 {
		t.Fatalf("database.json: %s", got["database.json"])
	}
	if !strings.Contains(got["logs/agentgo.log.tail.txt"], "calling model with api_key=sk-*** from C:\\Users\\<user>\\x") {
		t.Fatalf("the log tail is missing or not redacted: %q", got["logs/agentgo.log.tail.txt"])
	}
}

func keysOf(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestExportDiagnosticsWithoutADataDirSaysSo(t *testing.T) {
	svc := &AppService{rt: &Runtime{}}
	if got := svc.ExportDiagnostics(); got["success"] != false || got["error"] == nil {
		t.Fatalf("expected a refusal: %v", got)
	}
}

func TestGetAppInfoNamesTheBuild(t *testing.T) {
	svc := &AppService{rt: &Runtime{dataDir: "D:/data"}}
	info := svc.GetAppInfo()
	if info["version"] == "" || info["host"] != hostKind || info["data_dir"] != "D:/data" || info["go_version"] == "" {
		t.Fatalf("%v", info)
	}
}

func TestADiagnosticsBundleCarriesTheTraceOfTheLastCrash(t *testing.T) {
	svc, dir := diagnosticsService(t)
	crashText := "panic: runtime error: index out of range [3] with length 2\n\ngoroutine 7 [running]:\nmain.work()\n\tC:/Users/user/src/work.go:12 +0x1d\n"
	if err := os.WriteFile(filepath.Join(dir, "crash.log"), []byte(crashText), 0o644); err != nil {
		t.Fatal(err)
	}
	applog.SetQuiet(true)
	if err := applog.InitCrash(dir); err != nil { // the start after the crash
		t.Fatal(err)
	}
	defer func() { applog.CloseCrash(); applog.SetQuiet(false) }()

	info := svc.GetAppInfo()
	crash, ok := info["previous_crash"].(applog.CrashInfo)
	if !ok || !strings.HasPrefix(crash.Headline, "panic: runtime error") || crash.Size != int64(len(crashText)) {
		t.Fatalf("GetAppInfo does not report the crash: %v", info["previous_crash"])
	}

	res := svc.ExportDiagnostics()
	if res["success"] != true {
		t.Fatalf("export failed: %v", res)
	}
	got := readZip(t, res["path"].(string))
	trace, ok := got["logs/crash.last.txt"]
	if !ok {
		t.Fatalf("the bundle has no crash trace; have %v", keysOf(got))
	}
	if !strings.Contains(trace, "index out of range") || !strings.Contains(trace, "work.go:12") {
		t.Fatalf("the trace lost its content: %q", trace)
	}
	if strings.Contains(trace, `/Users/user/`) || strings.Contains(trace, `\Users\user\`) {
		t.Fatalf("the trace leaks the user name: %q", trace)
	}
}
