package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	bingsearch "github.com/cloudwego/eino-ext/components/tool/bingsearch"
	duckv2 "github.com/cloudwego/eino-ext/components/tool/duckduckgo/v2"
)

// registerDuckDuckGoSearch adds free web search via DuckDuckGo (no API key needed).
func registerDuckDuckGoSearch(r *Registry) error {
	t, err := duckv2.NewTextSearchTool(context.Background(), &duckv2.Config{
		ToolName:   "web_search",
		ToolDesc:   "Search the web using DuckDuckGo. Returns title, URL, and snippet for each result. Use for current events, documentation, or any information not in training data.",
		MaxResults: 8,
	})
	if err != nil {
		return fmt.Errorf("register web_search: %w", err)
	}
	r.AddTool(t)
	return nil
}

// registerBingSearch adds Bing search if BING_SEARCH_API_KEY is set.
func registerBingSearch(r *Registry) error {
	apiKey := strings.TrimSpace(os.Getenv("BING_SEARCH_API_KEY"))
	if apiKey == "" {
		return nil
	}
	t, err := bingsearch.NewTool(context.Background(), &bingsearch.Config{
		APIKey:     apiKey,
		ToolName:   "bing_search",
		ToolDesc:   "Search the web using Microsoft Bing. Use for high-quality, up-to-date web results.",
		MaxResults: 8,
	})
	if err != nil {
		return fmt.Errorf("register bing_search: %w", err)
	}
	r.AddTool(t)
	return nil
}
