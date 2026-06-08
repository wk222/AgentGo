package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/document/parser"
	"github.com/cloudwego/eino/components/tool/utils"
	docxparser "github.com/cloudwego/eino-ext/components/document/parser/docx"
	pdfparser "github.com/cloudwego/eino-ext/components/document/parser/pdf"
	xlsxparser "github.com/cloudwego/eino-ext/components/document/parser/xlsx"
)

type parseFileInput struct {
	RelativePath string `json:"relative_path" jsonschema:"required,description=Workspace-relative path (e.g. report.pdf or data/table.xlsx). Supports .pdf .docx .xlsx and plain text."`
}

type parseFileOutput struct {
	Text      string `json:"text"`
	Pages     int    `json:"pages,omitempty"`
	FileType  string `json:"file_type"`
	CharCount int    `json:"char_count"`
}

// registerParseFile registers a tool that reads PDF / DOCX / XLSX files from the workspace
// and returns their text content for the agent to reason over.
func registerParseFile(r *Registry, workspaceRoot string) error {
	root := workspaceRoot
	t, err := utils.InferTool("parse_file",
		"Read and extract text from a PDF, DOCX, or XLSX file in the workspace. "+
			"Returns the full text content so you can analyse, summarise, or quote from it. "+
			"Supports: .pdf, .docx, .xlsx, and plain-text files.",
		func(ctx context.Context, in parseFileInput) (parseFileOutput, error) {
			if in.RelativePath == "" {
				return parseFileOutput{}, fmt.Errorf("relative_path is required")
			}
			clean := filepath.Clean("/" + strings.ReplaceAll(in.RelativePath, "\\", "/"))
			abs := filepath.Join(root, clean)

			data, err := os.ReadFile(abs)
			if err != nil {
				return parseFileOutput{}, fmt.Errorf("read file: %w", err)
			}

			ext := strings.ToLower(filepath.Ext(abs))
			switch ext {
			case ".pdf":
				return parsePDF(ctx, data)
			case ".docx":
				return parseDOCX(ctx, data)
			case ".xlsx":
				return parseXLSX(ctx, data)
			default:
				text := string(data)
				return parseFileOutput{
					Text:      text,
					FileType:  strings.TrimPrefix(ext, "."),
					CharCount: len(text),
				}, nil
			}
		},
	)
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

func parsePDF(ctx context.Context, data []byte) (parseFileOutput, error) {
	p, err := pdfparser.NewPDFParser(ctx, nil)
	if err != nil {
		return parseFileOutput{}, fmt.Errorf("pdf parser init: %w", err)
	}
	docs, err := p.Parse(ctx, bytes.NewReader(data))
	if err != nil {
		return parseFileOutput{}, fmt.Errorf("pdf parse: %w", err)
	}
	var sb strings.Builder
	for _, d := range docs {
		sb.WriteString(d.Content)
		sb.WriteString("\n")
	}
	text := strings.TrimSpace(sb.String())
	return parseFileOutput{
		Text:      text,
		Pages:     len(docs),
		FileType:  "pdf",
		CharCount: len(text),
	}, nil
}

func parseDOCX(ctx context.Context, data []byte) (parseFileOutput, error) {
	p, err := docxparser.NewDocxParser(ctx, nil)
	if err != nil {
		return parseFileOutput{}, fmt.Errorf("docx parser init: %w", err)
	}
	docs, err := p.Parse(ctx, bytes.NewReader(data), parser.WithExtraMeta(nil))
	if err != nil {
		return parseFileOutput{}, fmt.Errorf("docx parse: %w", err)
	}
	var sb strings.Builder
	for _, d := range docs {
		sb.WriteString(d.Content)
		sb.WriteString("\n")
	}
	text := strings.TrimSpace(sb.String())
	return parseFileOutput{
		Text:      text,
		Pages:     len(docs),
		FileType:  "docx",
		CharCount: len(text),
	}, nil
}

func parseXLSX(ctx context.Context, data []byte) (parseFileOutput, error) {
	p, err := xlsxparser.NewXlsxParser(ctx, nil)
	if err != nil {
		return parseFileOutput{}, fmt.Errorf("xlsx parser init: %w", err)
	}
	docs, err := p.Parse(ctx, bytes.NewReader(data))
	if err != nil {
		return parseFileOutput{}, fmt.Errorf("xlsx parse: %w", err)
	}
	var sb strings.Builder
	for _, d := range docs {
		sb.WriteString(d.Content)
		sb.WriteString("\n")
	}
	text := strings.TrimSpace(sb.String())
	return parseFileOutput{
		Text:      text,
		Pages:     len(docs),
		FileType:  "xlsx",
		CharCount: len(text),
	}, nil
}
