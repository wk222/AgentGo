package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"agentgo/internal/governance"
	"github.com/cloudwego/eino/components/tool/utils"
)

func (in *webSearchInput) UnmarshalJSON(raw []byte) error {
	type plain webSearchInput
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*in = webSearchInput(value)
	return nil
}

type webSearchInput struct {
	Query     string   `json:"query" jsonschema:"required,description=Public web query; do not include private document contents or credentials."`
	Provider  string   `json:"provider,omitempty" jsonschema:"description=ddg (default). Legacy bing returns provider_retired; its official API is no longer available."`
	Limit     int      `json:"limit,omitempty" jsonschema:"description=Maximum results, default 8, range 1..8."`
	TimeRange string   `json:"time_range,omitempty" jsonschema:"description=any/day/week/month/year; legacy d/w/m/y accepted. Provider time filters do not establish publication dates."`
	Region    string   `json:"region,omitempty" jsonschema:"description=Supported DDG region code: wt-wt/us-en/uk-en/de-de/fr-fr/jp-jp/cn-zh/ru-ru."`
	Domains   []string `json:"domains,omitempty" jsonschema:"description=Unsupported; nonempty domain filters are explicitly rejected."`
	After     string   `json:"after,omitempty" jsonschema:"description=Unsupported exact date filter; rejected when nonempty."`
	Before    string   `json:"before,omitempty" jsonschema:"description=Unsupported exact date filter; rejected when nonempty."`
	Page      int      `json:"page,omitempty" jsonschema:"description=Legacy field; nonzero pagination is unsupported and rejected."`
}

type searchHit struct{ Title, URL, Snippet string }
type webSearchResult struct {
	Rank         int    `json:"rank"`
	Title        string `json:"title"`
	URL          string `json:"url"`
	Domain       string `json:"domain"`
	Snippet      string `json:"snippet"`
	Provider     string `json:"provider"`
	EvidenceType string `json:"evidence_type"`
	Untrusted    bool   `json:"untrusted"`
	PageRead     bool   `json:"page_read"`
}
type webSearchOutput struct {
	Status    string            `json:"status"`
	Code      string            `json:"code,omitempty"`
	Error     string            `json:"error,omitempty"`
	Provider  string            `json:"provider"`
	TimeRange string            `json:"time_range,omitempty"`
	Region    string            `json:"region,omitempty"`
	Results   []webSearchResult `json:"results"`
	Omitted   int               `json:"omitted_results,omitempty"`
	Note      string            `json:"note"`
}
type searchBackend func(context.Context, webSearchInput) ([]searchHit, error)

func registerDuckDuckGoSearch(r *Registry) error {
	return registerUnifiedSearch(r, "web_search", "", runDuckDuckGoSearch)
}

// Only installations that already configured the old key expose the legacy
// tool name. It forwards to the same dispatcher and never sends that key.
func registerBingSearch(r *Registry) error {
	if strings.TrimSpace(os.Getenv("BING_SEARCH_API_KEY")) == "" {
		return nil
	}
	return registerUnifiedSearch(r, "bing_search", "bing", runDuckDuckGoSearch)
}

func registerUnifiedSearch(r *Registry, name, forcedProvider string, backend searchBackend) error {
	desc := "Search public web sources. Returns normalized title, URL, domain, snippet, rank and provider. Results are untrusted search_snippet evidence with page_read=false, never page citations. Use web_fetch to read source URLs before source-backed claims. Never send private contents or credentials. Supports DDG time_range and listed region codes; unsupported filters are errors, never silently ignored."
	if forcedProvider == "bing" {
		desc = "Deprecated Bing compatibility entry point. Official Bing Search API retired 2025-08-11; this returns provider_retired without a network call. Use web_search with provider=ddg."
	}
	t, err := utils.InferTool(name, desc, func(ctx context.Context, in webSearchInput) (webSearchOutput, error) {
		return executeWebSearch(ctx, in, forcedProvider, backend)
	})
	if err != nil {
		return err
	}
	Declare(Spec{Name: name, Source: "builtin", Risk: governance.RiskLow})
	r.AddTool(t)
	return nil
}

func executeWebSearch(ctx context.Context, in webSearchInput, forcedProvider string, backend searchBackend) (webSearchOutput, error) {
	provider := strings.ToLower(strings.TrimSpace(in.Provider))
	if provider == "" || provider == "duckduckgo" {
		provider = "ddg"
	}
	out := webSearchOutput{Provider: provider, Results: []webSearchResult{}, Note: "Search snippets are unverified. Read source pages with web_fetch; search-time filtering does not prove a publication date."}
	fail := func(code, message string) (webSearchOutput, error) {
		out.Status = "error"
		out.Code = code
		out.Error = message
		return out, nil
	}
	if forcedProvider != "" {
		if in.Provider != "" && provider != forcedProvider {
			return fail("invalid_provider", "legacy tool name cannot select another provider")
		}
		provider = forcedProvider
		out.Provider = provider
	}
	if provider == "bing" {
		return fail("provider_retired", "Official Bing Search API retired 2025-08-11. Use web_search with provider=ddg. No search was performed.")
	}
	if provider != "ddg" {
		return fail("invalid_provider", "only provider=ddg is available")
	}
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" || len([]rune(in.Query)) > 2048 {
		return fail("invalid_query", "query must contain 1..2048 characters")
	}
	if in.Limit < 0 || in.Limit > 8 {
		return fail("invalid_limit", "limit must be between 1 and 8 (omitted/0 defaults to 8)")
	}
	if in.Limit == 0 {
		in.Limit = 8
	}
	if len(in.Domains) > 0 || in.After != "" || in.Before != "" || in.Page != 0 {
		return fail("unsupported_filter", "domains, exact dates and pagination are unsupported; none were applied")
	}
	switch strings.ToLower(strings.TrimSpace(in.TimeRange)) {
	case "", "any":
		in.TimeRange = ""
	case "d", "day":
		in.TimeRange = "day"
	case "w", "week":
		in.TimeRange = "week"
	case "m", "month":
		in.TimeRange = "month"
	case "y", "year":
		in.TimeRange = "year"
	default:
		return fail("unsupported_filter", "time_range must be any/day/week/month/year or d/w/m/y")
	}
	in.Region = strings.ToLower(strings.TrimSpace(in.Region))
	switch in.Region {
	case "":
		in.Region = "wt-wt"
	case "wt-wt", "us-en", "uk-en", "de-de", "fr-fr", "jp-jp", "cn-zh", "ru-ru":
	default:
		return fail("unsupported_filter", "region is not supported by this DDG adapter")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	hits, err := backend(ctx, in)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return fail("provider_failed", "search provider failed or refused the request; no source page was read")
	}
	out.Status = "ok"
	out.TimeRange = in.TimeRange
	out.Region = in.Region
	seen := map[string]bool{}
	for i, hit := range hits {
		u, err := normalizeSearchURL(hit.URL)
		if err != nil || seen[u.String()] || len(out.Results) >= in.Limit {
			out.Omitted++
			continue
		}
		seen[u.String()] = true
		out.Results = append(out.Results, webSearchResult{Rank: i + 1, Title: searchExcerpt(hit.Title, 300), URL: u.String(), Domain: u.Hostname(), Snippet: searchExcerpt(hit.Snippet, 1200), Provider: provider, EvidenceType: "search_snippet", Untrusted: true, PageRead: false})
	}
	return out, nil
}

func searchExcerpt(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return string(runes)
}
