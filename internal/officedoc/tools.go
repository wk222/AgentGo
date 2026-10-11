package officedoc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// Tool names, as the model sees them.
const (
	ToolCreateDocx = "create_docx"
	ToolCreateXlsx = "create_xlsx"
)

// Source names this package's tools in governance audit records.
const Source = "officedoc"

// maxMarkdownBytes bounds one document's source text.
const maxMarkdownBytes = 2 << 20

// Env is what the tools need from the application.
type Env struct {
	// Resolve turns a workspace-relative path into an absolute one inside the
	// current workspace, rejecting absolute paths and escapes. It is called on
	// every use, so a workspace switch needs no rebinding.
	Resolve func(rel string) (string, error)
}

type createDocxInput struct {
	RelativePath string `json:"relative_path" jsonschema:"required,description=Workspace-relative path of the .docx to create (e.g. reports/weekly.docx). The .docx extension is added when missing."`
	Markdown     string `json:"markdown" jsonschema:"required,description=Document content as Markdown: # headings (1-6)\\, paragraphs\\, **bold**\\, *italic*\\, ~~strike~~\\, inline code\\, links\\, nested - bullets and 1. numbered lists\\, | tables |\\, fenced code\\, > quotes\\, --- rules. A line with only <!-- pagebreak --> starts a new page."`
	Title        string `json:"title,omitempty" jsonschema:"description=Document title for the file properties. Defaults to the first heading."`
	Overwrite    bool   `json:"overwrite,omitempty" jsonschema:"description=Replace the file if it exists. Default false: an existing file is an error."`
}

type createXlsxInput struct {
	RelativePath string        `json:"relative_path" jsonschema:"required,description=Workspace-relative path of the .xlsx to create (e.g. data/budget.xlsx). The .xlsx extension is added when missing."`
	Sheets       []xlsxSheetIn `json:"sheets" jsonschema:"required,description=Worksheets in order. Each has a name and rows (a list of rows; each row a list of cell values)."`
	Overwrite    bool          `json:"overwrite,omitempty" jsonschema:"description=Replace the file if it exists. Default false: an existing file is an error."`
}

type xlsxSheetIn struct {
	Name   string  `json:"name" jsonschema:"description=Sheet name (max 31 chars)."`
	Rows   [][]any `json:"rows" jsonschema:"required,description=Rows of cells. Numbers and booleans stay typed; strings are text; null is an empty cell; a string starting with = is a formula (e.g. =SUM(B2:B9))."`
	Header *bool   `json:"header,omitempty" jsonschema:"description=Treat the first row as a header: bold\\, shaded\\, frozen and filterable. Default true."`
}

type createOutput struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	Detail string `json:"detail"`
}

// Tools returns the document-generation tools.
func Tools(env Env) ([]tool.BaseTool, error) {
	if env.Resolve == nil {
		return nil, errors.New("officedoc: Resolve is required")
	}
	docx, err := utils.InferTool(ToolCreateDocx,
		"Create a Word (.docx) document in the workspace from Markdown: reports, memos, meeting minutes, proposals. "+
			"Writes a real Word file (headings, lists, tables, bold/italic, code, page breaks) that opens in Word and WPS. "+
			"Fails if the file exists unless overwrite is true.",
		func(_ context.Context, in createDocxInput) (createOutput, error) {
			return createDocx(env, in)
		})
	if err != nil {
		return nil, err
	}
	xlsx, err := utils.InferTool(ToolCreateXlsx,
		"Create an Excel (.xlsx) workbook in the workspace: tables, budgets, schedules, data exports. "+
			"Numbers stay numeric, strings starting with = become formulas, and the first row is styled and frozen as a header. "+
			"Fails if the file exists unless overwrite is true.",
		func(_ context.Context, in createXlsxInput) (createOutput, error) {
			return createXlsx(env, in)
		})
	if err != nil {
		return nil, err
	}
	return []tool.BaseTool{docx, xlsx}, nil
}

func createDocx(env Env, in createDocxInput) (createOutput, error) {
	if strings.TrimSpace(in.Markdown) == "" {
		return createOutput{}, errors.New("markdown is required")
	}
	if len(in.Markdown) > maxMarkdownBytes {
		return createOutput{}, fmt.Errorf("markdown too large: %d bytes (max %d)", len(in.Markdown), maxMarkdownBytes)
	}
	rel := withExt(in.RelativePath, ".docx")
	abs, err := env.Resolve(rel)
	if err != nil {
		return createOutput{}, err
	}
	d := &Doc{Title: in.Title}
	first := AddMarkdown(d, in.Markdown)
	if d.Title == "" {
		d.Title = first
	}
	var buf bytes.Buffer
	if _, err := d.WriteTo(&buf); err != nil {
		return createOutput{}, err
	}
	if err := writeNew(abs, buf.Bytes(), in.Overwrite); err != nil {
		return createOutput{}, err
	}
	return createOutput{Path: filepath.ToSlash(rel), Bytes: buf.Len(), Detail: fmt.Sprintf("%d blocks", d.Blocks())}, nil
}

func createXlsx(env Env, in createXlsxInput) (createOutput, error) {
	if len(in.Sheets) == 0 {
		return createOutput{}, errors.New("sheets is required")
	}
	rel := withExt(in.RelativePath, ".xlsx")
	abs, err := env.Resolve(rel)
	if err != nil {
		return createOutput{}, err
	}
	sheets := make([]Sheet, len(in.Sheets))
	rows := 0
	for i, s := range in.Sheets {
		header := s.Header == nil || *s.Header
		sheets[i] = Sheet{Name: s.Name, Rows: s.Rows, Header: header}
		rows += len(s.Rows)
	}
	var buf bytes.Buffer
	if err := WriteXlsx(&buf, sheets); err != nil {
		return createOutput{}, err
	}
	if err := writeNew(abs, buf.Bytes(), in.Overwrite); err != nil {
		return createOutput{}, err
	}
	return createOutput{Path: filepath.ToSlash(rel), Bytes: buf.Len(), Detail: fmt.Sprintf("%d sheets, %d rows", len(sheets), rows)}, nil
}

func withExt(p, ext string) string {
	p = strings.TrimSpace(p)
	if !strings.EqualFold(filepath.Ext(p), ext) {
		p += ext
	}
	return p
}

// writeNew writes data to abs, creating parent directories. An existing file is
// an error unless overwrite is set; the write goes through a temp file so a
// failure never leaves a half-written document behind.
func writeNew(abs string, data []byte, overwrite bool) error {
	if st, err := os.Stat(abs); err == nil {
		if st.IsDir() {
			return fmt.Errorf("%s is a directory", filepath.Base(abs))
		}
		if !overwrite {
			return fmt.Errorf("%s already exists; set overwrite to replace it", filepath.Base(abs))
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".officedoc-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(name, abs); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
