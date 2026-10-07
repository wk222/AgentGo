package spill

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type fetchSpillInput struct {
	SpillID     string `json:"spill_id" jsonschema:"required,description=The spill ID to retrieve (e.g. spill_abc123)"`
	OffsetLine  int    `json:"offset_line,omitempty" jsonschema:"description=1-based line number to start reading from"`
	LimitLines  int    `json:"limit_lines,omitempty" jsonschema:"description=Maximum number of lines to return (default 100)"`
	RegexFilter string `json:"regex_filter,omitempty" jsonschema:"description=Optional regular expression to filter lines"`
}

type fetchSpillOutput struct {
	SpillID       string `json:"spill_id"`
	TotalLines    int    `json:"total_lines"`
	OffsetLine    int    `json:"offset_line"`
	ReturnedLines int    `json:"returned_lines"`
	HasMore       bool   `json:"has_more"`
	Content       string `json:"content"`
}

// NewFetchSpillTool creates an invokable tool for inspecting offloaded spill content.
func NewFetchSpillTool(store Store) (tool.InvokableTool, error) {
	if store == nil {
		return nil, fmt.Errorf("spill: store is required for fetch tool")
	}

	return utils.InferTool(
		"fetch_spill_content",
		"Retrieve sliced or filtered content from a spilled/offloaded tool output by its spill_id.",
		func(ctx context.Context, in fetchSpillInput) (fetchSpillOutput, error) {
			res, err := store.Fetch(ctx, in.SpillID, FetchOptions{
				OffsetLine:  in.OffsetLine,
				LimitLines:  in.LimitLines,
				RegexFilter: in.RegexFilter,
			})
			if err != nil {
				return fetchSpillOutput{}, err
			}
			return fetchSpillOutput{
				SpillID:       res.ID,
				TotalLines:    res.TotalLines,
				OffsetLine:    res.OffsetLine,
				ReturnedLines: res.ReturnedLines,
				HasMore:       res.HasMore,
				Content:       res.Content,
			}, nil
		},
	)
}
