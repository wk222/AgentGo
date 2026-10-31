package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	duckv2 "github.com/cloudwego/eino-ext/components/tool/duckduckgo/v2"
)

func runDuckDuckGoSearch(ctx context.Context, in webSearchInput) ([]searchHit, error) {
	return duckDuckGoSearchWithClient(ctx, in, newSearchHTTPClient(http.DefaultTransport))
}

func duckDuckGoSearchWithClient(ctx context.Context, in webSearchInput, client *http.Client) ([]searchHit, error) {
	search, err := duckv2.NewSearch(ctx, &duckv2.Config{MaxResults: in.Limit, Region: duckv2.Region(in.Region), HTTPClient: client})
	if err != nil {
		return nil, err
	}
	timeRange := map[string]duckv2.TimeRange{"": duckv2.TimeRangeAny, "day": duckv2.TimeRangeDay, "week": duckv2.TimeRangeWeek, "month": duckv2.TimeRangeMonth, "year": duckv2.TimeRangeYear}[in.TimeRange]
	response, err := search.TextSearch(ctx, &duckv2.TextSearchRequest{Query: in.Query, TimeRange: timeRange})
	if err != nil {
		return nil, err
	}
	var hits []searchHit
	for _, result := range response.Results {
		if result != nil {
			hits = append(hits, searchHit{Title: result.Title, URL: result.URL, Snippet: result.Summary})
		}
	}
	return hits, nil
}

// The pinned adapter does not close response bodies or bound io.ReadAll. This
// transport closes the network body immediately and supplies bounded bytes.
type searchTransport struct{ base http.RoundTripper }

func (t searchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if len(raw) > 2*1024*1024 {
		return nil, fmt.Errorf("search response too large")
	}
	if bytes.Contains(raw, []byte("anomaly-modal")) || bytes.Contains(raw, []byte("challenge-form")) {
		return nil, fmt.Errorf("search provider requires manual verification")
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return resp, nil
}

func newSearchHTTPClient(base http.RoundTripper) *http.Client {
	return &http.Client{Timeout: 20 * time.Second, Transport: searchTransport{base: base}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		host := strings.ToLower(req.URL.Hostname())
		if len(via) > 5 || req.URL.Scheme != "https" || (host != "duckduckgo.com" && host != "html.duckduckgo.com" && host != "www.duckduckgo.com") {
			return fmt.Errorf("search redirect refused")
		}
		return nil
	}}
}

func normalizeSearchURL(raw string) (*url.URL, error) {
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if (strings.EqualFold(u.Hostname(), "duckduckgo.com") || strings.EqualFold(u.Hostname(), "www.duckduckgo.com")) && u.Path == "/l/" && u.Query().Get("uddg") != "" {
		raw = u.Query().Get("uddg")
	}
	if len(raw) > 2048 {
		return nil, fmt.Errorf("search URL too long")
	}
	return normalizeWebURL(raw)
}
