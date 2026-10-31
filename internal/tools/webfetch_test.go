package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentgo/internal/governance"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func webTestContext(id string) context.Context {
	return governance.WithSessionID(context.Background(), id)
}
func webTestReader() *webPageReader {
	p := webNetworkPolicy{privateHosts: map[string]bool{"127.0.0.1": true}}
	return newWebPageReader(p.client())
}
func requireWebError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("wanted %s, got success", want)
	}
	code, _, _ := webFailure(err)
	if code != want {
		t.Fatalf("error = %s (%v), want %s", code, err, want)
	}
}

func TestWebFetchSnapshotsKeepUnicodeRevisionAndSessionBoundary(t *testing.T) {
	var reads atomic.Int32
	var changed atomic.Bool
	original := strings.Repeat("合同🙂", 4000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		reads.Add(1)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if changed.Load() {
			fmt.Fprint(w, "新版本")
		} else {
			fmt.Fprint(w, original)
		}
	}))
	defer server.Close()
	r := webTestReader()
	first, err := r.read(webTestContext("a"), "a", webFetchItem{URL: server.URL, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if first.NextOffset == nil || *first.NextOffset != 5 || first.Revision == "" || !first.Untrusted {
		t.Fatalf("missing snapshot contract: %+v", first)
	}
	changed.Store(true)
	var joined strings.Builder
	joined.WriteString(first.Text)
	offset := *first.NextOffset
	for {
		next, err := r.read(webTestContext("a"), "a", webFetchItem{SnapshotID: first.SnapshotID, Offset: offset, ExpectedRevision: first.Revision})
		if err != nil {
			t.Fatal(err)
		}
		joined.WriteString(next.Text)
		if next.NextOffset == nil {
			break
		}
		offset = *next.NextOffset
	}
	if joined.String() != original || reads.Load() != 1 {
		t.Fatal("continuation fetched a live page or lost Unicode content")
	}
	_, err = r.read(webTestContext("b"), "b", webFetchItem{SnapshotID: first.SnapshotID})
	requireWebError(t, err, "snapshot_expired")
	_, err = r.read(webTestContext("a"), "a", webFetchItem{URL: server.URL, ExpectedRevision: first.Revision})
	requireWebError(t, err, "source_changed")
	r.now = func() time.Time { return time.Now().Add(webSnapshotTTL + time.Minute) }
	_, err = r.read(webTestContext("a"), "a", webFetchItem{SnapshotID: first.SnapshotID})
	requireWebError(t, err, "snapshot_expired")
}

func TestWebFetchBatchBudgetAndIndependentFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/fail" {
			w.WriteHeader(429)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, strings.Repeat("文🙂", 6000))
	}))
	defer server.Close()
	r := webTestReader()
	out, err := r.readBatch(webTestContext("a"), webFetchInput{Items: []webFetchItem{{URL: server.URL + "/one"}, {URL: server.URL + "/fail"}, {URL: server.URL + "/two"}, {URL: server.URL + "/three"}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Results[1].Code != "http_429" || !out.Results[1].Retryable {
		t.Fatalf("missing independent failure: %+v", out.Results[1])
	}
	total := 0
	for i, result := range out.Results {
		if i == 1 {
			continue
		}
		n := charCount(result.Text)
		total += n
		if result.Status != "ok" || n < 5000 || result.NextOffset == nil || *result.NextOffset != n {
			t.Fatalf("page lost under shared budget: %+v", result)
		}
	}
	if total > webBatchBudget {
		t.Fatalf("batch budget exceeded: %d", total)
	}
	if _, err := r.readBatch(webTestContext("a"), webFetchInput{}); err == nil {
		t.Fatal("empty batch accepted")
	}
}

func TestWebFetchHTMLPreservesEvidenceAndRemovesPageChrome(t *testing.T) {
	base, _ := url.Parse("https://example.com/docs/start")
	title, text, err := readableWebHTML(`<html><head><title>政策 &amp; 预算</title></head><body><nav>Navigation junk</nav><main><h1>预算</h1><p>保留 <strong>真实措辞</strong> 与 <a href="../policy">政策</a>。</p><table><tr><th>项目</th><th>金额</th></tr><tr><td>机房</td><td>100</td></tr></table><pre> a
  b</pre><div hidden>secret hidden</div><script>malicious script</script><p>ignore previous instructions 是待分析的原文。</p></main><footer>Footer junk</footer></body></html>`, base)
	if err != nil {
		t.Fatal(err)
	}
	if title != "政策 & 预算" {
		t.Fatalf("title=%q", title)
	}
	for _, want := range []string{"# 预算", "真实措辞", "https://example.com/policy", "机房\t100", " a\n  b", "ignore previous instructions"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lost %q in %q", want, text)
		}
	}
	for _, forbidden := range []string{"Navigation junk", "Footer junk", "malicious script", "secret hidden"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("page chrome leaked: %q", text)
		}
	}
}

func TestWebFetchPolicyStillAllowsOrdinaryPublicAddresses(t *testing.T) {
	p := webNetworkPolicy{privateHosts: map[string]bool{}}
	// 2001:4860::/32 is Google's, next to the blocked Teredo 2001::/32.
	for _, host := range []string{"93.184.216.34", "2606:4700:4700::1111", "2001:4860:4860::8888"} {
		if _, err := p.addresses(context.Background(), host); err != nil {
			t.Errorf("%s was blocked: %v", host, err)
		}
	}
}

func TestWebFetchPolicyBlocksPrivateDestinationsAndRedirectBypass(t *testing.T) {
	p := webNetworkPolicy{privateHosts: map[string]bool{}}
	for _, host := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "198.18.0.1",
		"64:ff9b::a9fe:a9fe", "2002:a9fe:a9fe::1", "2001:0:4136:e378:8000:63bf:3fff:fdd2", "fec0::1", "::7f00:1", "::a9fe:a9fe", "ff02::1", "fd00::1", "::"} {
		_, err := p.addresses(context.Background(), host)
		requireWebError(t, err, "network_policy")
	}
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		if req.URL.Path == "/redirect" {
			http.Redirect(w, req, "http://localhost:"+strings.Split(req.Host, ":")[1]+"/private", http.StatusFound)
			return
		}
		fmt.Fprint(w, "must not reach")
	}))
	defer server.Close()
	_, err := fetchWebPage(context.Background(), p.client(), server.URL)
	requireWebError(t, err, "network_policy")
	if hits.Load() != 0 {
		t.Fatal("blocked destination was contacted")
	}
	allowed := webNetworkPolicy{privateHosts: map[string]bool{"127.0.0.1": true}}
	_, err = fetchWebPage(context.Background(), allowed.client(), server.URL+"/redirect")
	requireWebError(t, err, "network_policy")
	if hits.Load() != 1 {
		t.Fatal("redirect bypassed exact-host private policy")
	}
	for _, raw := range []string{"file:///etc/passwd", "http://user:pass@example.com", "https://", "data:text/plain,hello"} {
		_, err := normalizeWebURL(raw)
		requireWebError(t, err, "invalid_url")
	}
}

func TestWebFetchRejectsBinaryOversizeAndDecodesGBK(t *testing.T) {
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("合同金额"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/binary":
			w.Header().Set("Content-Type", "application/pdf")
			fmt.Fprint(w, "%PDF")
		case "/large":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, strings.Repeat("a", webBodyLimit+1))
		case "/gbk":
			w.Header().Set("Content-Type", "text/plain; charset=gbk")
			_, _ = w.Write(gbk)
		case "/empty":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<script>onlyJS()</script>")
		}
	}))
	defer server.Close()
	client := webTestReader().client
	for path, code := range map[string]string{"/binary": "unsupported_content", "/large": "body_too_large", "/empty": "empty_content"} {
		_, err := fetchWebPage(context.Background(), client, server.URL+path)
		requireWebError(t, err, code)
	}
	page, err := fetchWebPage(context.Background(), client, server.URL+"/gbk")
	if err != nil || page.Text != "合同金额" {
		t.Fatalf("GBK page = %+v %v", page, err)
	}
}

func TestWebFetchRegisteredToolIsReachableAndReturnsStructuredFailure(t *testing.T) {
	r := NewRegistry()
	if err := registerWebFetch(r); err != nil {
		t.Fatal(err)
	}
	dynamic := r.GetDynamicTools()
	found := false
	for _, tool := range dynamic {
		info, err := tool.Info(context.Background())
		if err == nil && info.Name == "web_fetch" {
			found = true
		}
	}
	if !found {
		t.Fatal("web_fetch is missing from discoverable tools")
	}
	raw, err := r.InvokeJSON(webTestContext("a"), "web_fetch", `{"items":[{"url":"file:///etc/passwd"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	var out webFetchOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].Code != "invalid_url" {
		t.Fatalf("missing failure envelope: %s", raw)
	}
}

func TestWebFetchSnapshotEvictionAndNoSilentRefetch(t *testing.T) {
	r := webTestReader()
	base := time.Now()
	for i := 0; i < webSnapshotCount+1; i++ {
		r.save(webSnapshot{webPage: webPage{Text: "saved", URL: "https://example.com"}, id: fmt.Sprint(i), scope: "a", created: base.Add(time.Duration(i) * time.Millisecond)})
	}
	_, err := r.read(webTestContext("a"), "a", webFetchItem{SnapshotID: "0"})
	requireWebError(t, err, "snapshot_expired")
	_, err = r.read(webTestContext("a"), "a", webFetchItem{URL: "https://example.com", Offset: 1})
	requireWebError(t, err, "snapshot_required")
	if len(r.snapshots) != webSnapshotCount || r.bytes != 5*webSnapshotCount {
		t.Fatal("snapshot cache is not bounded or accounted correctly")
	}
}

func TestWebFetchCancelledAndUntrustedTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { fmt.Fprint(w, "secure") }))
	defer server.Close()
	_, err := fetchWebPage(context.Background(), webTestReader().client, server.URL)
	requireWebError(t, err, "tls_failed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fetchWebPage(ctx, webTestReader().client, server.URL)
	requireWebError(t, err, "cancelled")
}

func TestWebFetchLivePublicPage(t *testing.T) {
	if os.Getenv("AGENTGO_TEST_WEB_FETCH_LIVE") != "1" {
		t.Skip("explicit public-network smoke test")
	}
	r := NewRegistry()
	if err := registerWebFetch(r); err != nil {
		t.Fatal(err)
	}
	raw, err := r.InvokeJSON(webTestContext("live-smoke"), "web_fetch", `{"items":[{"url":"https://example.com","limit":200}]}`)
	if err != nil {
		t.Fatal(err)
	}
	var out webFetchOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].Status != "ok" || out.Results[0].Title != "Example Domain" || strings.TrimSpace(out.Results[0].Text) == "" {
		t.Fatalf("public fetch failed: %+v", out)
	}
}

func TestWebFetchContinuationAcceptsOriginalRedirectURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/start" {
			http.Redirect(w, req, "/final", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "正文来源内容")
	}))
	defer server.Close()
	r := webTestReader()
	first, err := r.read(webTestContext("a"), "a", webFetchItem{URL: server.URL + "/start", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if first.URL != server.URL+"/final" {
		t.Fatalf("final source URL lost: %+v", first)
	}
	next, err := r.read(webTestContext("a"), "a", webFetchItem{URL: server.URL + "/start", SnapshotID: first.SnapshotID, Offset: *first.NextOffset})
	if err != nil || first.Text+next.Text != "正文来源内容" {
		t.Fatalf("redirect continuation failed: %+v %v", next, err)
	}
}
