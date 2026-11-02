package coderuntime

// view: read a slice of a text file with line numbers.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type viewArgs struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

func (t *fsTools) viewTool() Tool {
	return Tool{
		Name: "view",
		Description: fmt.Sprintf("Show the contents of a text file with line numbers. "+
			"You get %d lines from `offset` (a count of lines to skip, so 0 means the top) unless `limit` asks for another amount; "+
			"if the file goes on, the output ends with the offset to continue from. "+
			"A line over %d characters is cut short and one call never returns more than %d KB. "+
			"View a file before you edit or overwrite it: the edit tools refuse otherwise.",
			DefaultReadLimit, MaxLineLength, MaxViewSize/1024),
		Schema: objectSchema(map[string]any{
			"file_path": strProp("Path of the file to show"),
			"offset":    intProp("How many lines to skip before the first one shown (0 = start at the top)"),
			"limit":     intProp(fmt.Sprintf("How many lines to show (default %d)", DefaultReadLimit)),
		}, "file_path"),
		Parallel: true,
		Run: func(ctx context.Context, sessionID, callID, argsJSON string) (ToolResult, error) {
			var a viewArgs
			if r, bad := parseArgs(argsJSON, &a); bad {
				return r, nil
			}
			return t.view(ctx, sessionID, callID, a)
		},
	}
}

func (t *fsTools) view(ctx context.Context, sessionID, callID string, a viewArgs) (ToolResult, error) {
	if a.FilePath == "" {
		return needFilePath(), nil
	}
	path := t.resolve(a.FilePath)
	abs, err := filepath.Abs(path)
	if err != nil {
		return ToolResult{}, fmt.Errorf("cannot resolve %q: %w", a.FilePath, err)
	}
	if t.outsideWorkDir(abs) {
		req := FSRequest{SessionID: sessionID, CallID: callID, Tool: "view", Action: "read", Path: abs,
			Description: "Read a file outside the working directory: " + abs}
		if !t.approve(ctx, req) {
			return errResult(refusedByUser), nil
		}
	}

	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		return missingFile(path, similarNames(path)), nil
	case err != nil:
		return ToolResult{}, fmt.Errorf("cannot stat %s: %w", path, err)
	case info.IsDir():
		return isDirectory(path), nil
	case isImagePath(path):
		return errResult(path + " is an image; view only shows text files."), nil
	}

	limit := a.Limit
	if limit <= 0 {
		limit = DefaultReadLimit
	}
	skip := max(a.Offset, 0)
	lines, more, err := sliceLines(path, skip, limit)
	var big sliceTooBig
	switch {
	case errors.As(err, &big):
		return errResult(fmt.Sprintf("That slice is %d bytes, over the %d byte limit. Ask for fewer lines with `limit`, or start further down with `offset`.",
			big.size, big.limit)), nil
	case err != nil:
		return ToolResult{}, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if !utf8.ValidString(strings.Join(lines, "\n")) {
		return errResult(path + " is not valid UTF-8 text, so it cannot be shown."), nil
	}

	t.markViewed(sessionID, path)
	if t.opts.OnRead != nil {
		t.opts.OnRead(ctx, path)
	}
	return okResult(renderSlice(path, skip, lines, more)), nil
}

// renderSlice lays out the lines as "<number><TAB><text>" under a header that
// says which lines are shown, and ends with the offset to continue from.
func renderSlice(path string, skip int, lines []string, more bool) string {
	var b strings.Builder
	if len(lines) == 0 {
		if skip > 0 {
			fmt.Fprintf(&b, "%s: the file has no line after the first %d.\n", path, skip)
		} else {
			fmt.Fprintf(&b, "%s: the file is empty.\n", path)
		}
		return b.String()
	}
	fmt.Fprintf(&b, "%s (lines %d-%d)\n", path, skip+1, skip+len(lines))
	for i, ln := range lines {
		fmt.Fprintf(&b, "%6d\t%s\n", skip+1+i, ln)
	}
	if more {
		fmt.Fprintf(&b, "... the file continues; call view again with offset=%d\n", skip+len(lines))
	}
	return b.String()
}

type sliceTooBig struct{ size, limit int }

func (e sliceTooBig) Error() string {
	return fmt.Sprintf("slice of %d bytes exceeds the %d byte limit", e.size, e.limit)
}

// sliceLines skips `skip` lines, then collects up to `take`, enforcing the byte
// budget on what it returns. more reports whether text remains after the slice.
func sliceLines(path string, skip, take int) (lines []string, more bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	rd := bufio.NewReader(f)

	next := func() (string, error) {
		s, err := rd.ReadString('\n')
		if err == io.EOF && s != "" {
			err = nil // a last line without a newline
			s += "\n"
		}
		return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r"), err
	}

	for i := 0; i < skip; i++ {
		if _, err := next(); err != nil {
			if err == io.EOF {
				return nil, false, nil
			}
			return nil, false, err
		}
	}
	used := 0
	for len(lines) < take {
		ln, err := next()
		if err == io.EOF {
			return lines, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if len(ln) > MaxLineLength {
			ln = strings.ToValidUTF8(ln[:MaxLineLength], "") + " [line cut]"
		}
		cost := len(ln) + 1
		if used+cost > MaxViewSize {
			return nil, false, sliceTooBig{size: used + cost, limit: MaxViewSize}
		}
		used += cost
		lines = append(lines, ln)
	}
	// Peek one more byte to learn whether the file really goes on.
	_, perr := rd.Peek(1)
	return lines, perr == nil, nil
}

func isImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		return true
	}
	return false
}

// similarNames lists up to three files next to path whose names contain, or are
// contained in, the requested name; it helps when the model got a name slightly
// wrong.
func similarNames(path string) []string {
	want := strings.ToLower(filepath.Base(path))
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		have := strings.ToLower(e.Name())
		if strings.Contains(have, want) || strings.Contains(want, have) {
			out = append(out, filepath.Join(filepath.Dir(path), e.Name()))
			if len(out) == 3 {
				break
			}
		}
	}
	return out
}
