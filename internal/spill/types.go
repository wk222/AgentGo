package spill

import (
	"context"
	"time"
)

// SpillRecord holds metadata and content location of an offloaded tool output.
type SpillRecord struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	TotalBytes int64     `json:"total_bytes"`
	TotalLines int       `json:"total_lines"`
	FilePath   string    `json:"file_path"`
	CreatedAt  time.Time `json:"created_at"`
}

// FetchOptions specifies slicing and filtering options when retrieving spilled content.
type FetchOptions struct {
	OffsetLine  int    `json:"offset_line,omitempty"`
	LimitLines  int    `json:"limit_lines,omitempty"`
	RegexFilter string `json:"regex_filter,omitempty"`
}

// FetchResult contains the sliced content and pagination metadata.
type FetchResult struct {
	ID          string `json:"id"`
	TotalLines  int    `json:"total_lines"`
	TotalBytes  int64  `json:"total_bytes"`
	OffsetLine  int    `json:"offset_line"`
	ReturnedLines int  `json:"returned_lines"`
	HasMore     bool   `json:"has_more"`
	Content     string `json:"content"`
}

// Store persists large tool outputs and provides random-access slicing.
type Store interface {
	Save(ctx context.Context, sessionID, toolCallID, toolName string, rawContent []byte) (*SpillRecord, error)
	Fetch(ctx context.Context, spillID string, opts FetchOptions) (*FetchResult, error)
	GetRecord(ctx context.Context, spillID string) (*SpillRecord, error)
	Delete(ctx context.Context, spillID string) error
	CleanupSession(ctx context.Context, sessionID string) error
}
