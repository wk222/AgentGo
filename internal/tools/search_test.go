package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

type searchRoundTrip func(*http.Request) (*http.Response, error)

func (f searchRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type searchBody struct {
	io.Reader
	closed *bool
}

func (b searchBody) Close() error { *b.closed = true; return nil }

func TestUnifiedSearchRealDDGParserAndAppliedFilters(t *testing.T) {
	closed := false
	client := newSearchHTTPClient(searchRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://html.duckduckgo.com/html/" {
			t.Fatal("unexpected provider URL")
		}
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		body, err := url.ParseQuery(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		if body.Get("q") != "公开查询" || body.Get("df") != "w" || body.Get("kl") != "cn-zh" {
			t.Fatalf("filters not applied: %v", body)
		}
		html := `<div id="links">` +
			`<div class="web-result"><h2 class="result__title"><a href="https://Example.com/a#one">First</a></h2><a class="result__snippet">原始搜索摘要</a></div>` +
			`<div class="web-result"><h2 class="result__title"><a href="https://example.com/a#two">Duplicate</a></h2><a class="result__snippet">duplicate</a></div>` +
			`<div class="web-result"><h2 class="result__title"><a href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fother.example%2Fb">Second</a></h2><a class="result__snippet">another snippet</a></div>` +
			`<div class="web-result"><h2 class="result__title"><a href="javascript:alert(1)">Invalid</a></h2><a class="result__snippet">invalid scheme</a></div></div>`
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: searchBody{strings.NewReader(html), &closed}, Request: req}, nil
	}))
	out, err := executeWebSearch(context.Background(), webSearchInput{Query: "公开查询", TimeRange: "w", Region: "cn-zh"}, "", func(ctx context.Context, in webSearchInput) ([]searchHit, error) {
		return duckDuckGoSearchWithClient(ctx, in, client)
	})
	if err != nil || out.Status != "ok" || len(out.Results) != 2 || out.Omitted != 2 || !closed {
		t.Fatalf("bad search: %+v %v closed=%v", out, err, closed)
	}
	if out.Results[0].URL != "https://example.com/a" || out.Results[1].URL != "https://other.example/b" || out.Results[1].Rank != 3 {
		t.Fatal(out.Results)
	}
	for _, r := range out.Results {
		if r.PageRead || !r.Untrusted || r.EvidenceType != "search_snippet" || r.Provider != "ddg" {
			t.Fatal("snippet promoted to page evidence")
		}
	}
	if out.TimeRange != "week" || out.Region != "cn-zh" {
		t.Fatal("applied filters missing")
	}
}

func TestUnifiedSearchRejectsUnsupportedRequestsBeforeNetwork(t *testing.T) {
	called := false
	backend := func(context.Context, webSearchInput) ([]searchHit, error) { called = true; return nil, nil }
	for _, in := range []webSearchInput{{Query: "q", Provider: "unknown"}, {Query: ""}, {Query: "q", Limit: 9}, {Query: "q", Limit: -1}, {Query: "q", TimeRange: "last decade"}, {Query: "q", Region: "unknown"}, {Query: "q", Domains: []string{"example.com"}}, {Query: "q", After: "2026-10-01"}, {Query: "q", Before: "2026-10-29"}, {Query: "q", Page: 1}} {
		out, err := executeWebSearch(context.Background(), in, "", backend)
		if err != nil || out.Status != "error" || out.Code == "" || called {
			t.Fatal("unsupported request dispatched", in, out, err)
		}
	}
}

func TestUnifiedSearchRegisteredLegacyAndStrictSchema(t *testing.T) {
	r := NewRegistry()
	backend := func(context.Context, webSearchInput) ([]searchHit, error) {
		return []searchHit{{Title: "Title", URL: "https://example.com", Snippet: "snippet"}}, nil
	}
	if err := registerUnifiedSearch(r, "web_search", "", backend); err != nil {
		t.Fatal(err)
	}
	if err := registerUnifiedSearch(r, "bing_search", "bing", func(context.Context, webSearchInput) ([]searchHit, error) {
		t.Fatal("retired Bing network call")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"web_search", "bing_search"} {
		bt, _ := r.Get(name)
		info, err := bt.Info(context.Background())
		if err != nil || info.ParamsOneOf == nil {
			t.Fatal("missing schema", err)
		}
		raw, err := bt.(tool.InvokableTool).InvokableRun(context.Background(), `{"query":"q"}`)
		if err != nil {
			t.Fatal(err)
		}
		var out webSearchOutput
		if err = json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		if name == "bing_search" && out.Code != "provider_retired" {
			t.Fatal(out)
		}
		if name == "web_search" && (out.Status != "ok" || len(out.Results) != 1) {
			t.Fatal(out)
		}
	}
	bt, _ := r.Get("web_search")
	if _, err := bt.(tool.InvokableTool).InvokableRun(context.Background(), `{"query":"q","date_from":"ignored?"}`); err == nil {
		t.Fatal("unknown filter silently ignored")
	}
}

func TestUnifiedSearchFailureCancellationAndBounds(t *testing.T) {
	out, err := executeWebSearch(context.Background(), webSearchInput{Query: "private-test-marker"}, "", func(context.Context, webSearchInput) ([]searchHit, error) {
		return nil, errors.New("key=secret-test-marker private-test-marker")
	})
	raw, _ := json.Marshal(out)
	if err != nil || out.Code != "provider_failed" || strings.Contains(string(raw), "test-marker") {
		t.Fatal("provider detail leaked", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = executeWebSearch(ctx, webSearchInput{Query: "q"}, "", nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation swallowed", err)
	}
	out, err = executeWebSearch(context.Background(), webSearchInput{Query: "q", Limit: 1}, "", func(context.Context, webSearchInput) ([]searchHit, error) {
		return []searchHit{{Title: strings.Repeat("中😀", 400), URL: "https://example.com", Snippet: strings.Repeat("中😀", 1000)}, {URL: "https://other.example"}}, nil
	})
	if err != nil || len(out.Results) != 1 || out.Omitted != 1 || len([]rune(out.Results[0].Title)) != 301 || len([]rune(out.Results[0].Snippet)) != 1201 {
		t.Fatal("unbounded search output", err)
	}
}

func TestSearchTransportBoundsChallengesAndRedirects(t *testing.T) {
	for _, body := range []string{strings.Repeat("x", 2*1024*1024+1), `<form id="challenge-form">captcha</form>`} {
		closed := false
		client := newSearchHTTPClient(searchRoundTrip(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: searchBody{strings.NewReader(body), &closed}, Request: req}, nil
		}))
		if _, err := client.Get("https://html.duckduckgo.com/html/"); err == nil || !closed {
			t.Fatal("unsafe provider body accepted or not closed")
		}
	}
	calls := 0
	client := newSearchHTTPClient(searchRoundTrip(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"http://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	}))
	if _, err := client.Get("https://html.duckduckgo.com/html/"); err == nil || calls != 1 {
		t.Fatal("search followed unexpected redirect")
	}
}

func TestUnifiedSearchLive(t *testing.T) {
	if os.Getenv("AGENTGO_TEST_SEARCH_LIVE") != "1" {
		t.Skip("opt-in public search smoke")
	}
	r := NewRegistry()
	if err := registerDuckDuckGoSearch(r); err != nil {
		t.Fatal(err)
	}
	bt, _ := r.Get("web_search")
	raw, err := bt.(tool.InvokableTool).InvokableRun(context.Background(), `{"query":"example.com example domain","limit":3}`)
	if err != nil {
		t.Fatal(err)
	}
	var out webSearchOutput
	if err = json.Unmarshal([]byte(raw), &out); err != nil || out.Status != "ok" || len(out.Results) == 0 {
		t.Fatalf("search unavailable: %s %v", raw, err)
	}
	t.Logf("live provider=%s results=%d", out.Provider, len(out.Results))
	if strings.Contains(raw, `"citation"`) {
		t.Fatal("search minted page evidence")
	}
	if err = registerWebFetch(r); err != nil {
		t.Fatal(err)
	}
	fetch, _ := r.Get("web_fetch")
	var items []webFetchItem
	for _, result := range out.Results {
		items = append(items, webFetchItem{URL: result.URL, Limit: 2000})
	}
	args, _ := json.Marshal(webFetchInput{Items: items})
	pageRaw, err := fetch.(tool.InvokableTool).InvokableRun(WithEvidenceRun(context.Background()), string(args))
	if err != nil {
		t.Fatal(err)
	}
	var pages webFetchOutput
	if err = json.Unmarshal([]byte(pageRaw), &pages); err != nil {
		t.Fatal(err)
	}
	verified := 0
	for i, page := range pages.Results {
		if page.Status == "ok" && page.Citation != nil && page.EvidenceType == "page_content" {
			verified++
			t.Logf("live source read: domain=%s citation=true text_chars=%d", out.Results[i].Domain, charCount(page.Text))
		} else {
			t.Logf("live source unavailable: domain=%s code=%s", out.Results[i].Domain, page.Code)
		}
	}
	if verified == 0 {
		t.Fatal("none of the actual search results could be read as source evidence")
	}
}
