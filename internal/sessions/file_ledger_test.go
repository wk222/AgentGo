package sessions

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func toolCall(id, name, args string) *schema.Message {
	return schema.AssistantMessage("", []schema.ToolCall{{ID: id, Function: schema.FunctionCall{Name: name, Arguments: args}}})
}

// Run many rounds through repeated projection, the way a long session does.
func projectRounds(t *testing.T, first []*schema.Message, rounds int, call func(i int) (*schema.Message, string)) string {
	t.Helper()
	msgs := append([]*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("write the quarterly report, then keep working")}, first...)
	for i := 0; i < rounds; i++ {
		m, result := call(i)
		msgs = append(msgs, m, schema.ToolMessage(result, fmt.Sprint(m.ToolCalls[0].ID)))
		var err error
		msgs, err = CompileProjectedView(msgs, CompileOptions{MaxActiveTurns: 6, PreserveLatestUser: true}).Render(context.Background())
		if err != nil {
			t.Fatal(err)
		}
	}
	return renderedText(msgs)
}

func TestWrittenFilePathSurvivesManyLargeToolResults(t *testing.T) {
	const report = `C:\work\quarterly\report.docx`
	first := []*schema.Message{
		toolCall("w", "write", `{"path":"C:\\work\\quarterly\\report.docx","content":"draft"}`),
		schema.ToolMessage("ok", "w"),
	}
	out := projectRounds(t, first, 60, func(i int) (*schema.Message, string) {
		id := fmt.Sprint("r", i)
		return toolCall(id, "read_file", fmt.Sprintf(`{"path":"C:\\work\\src\\file%02d.go"}`, i)), strings.Repeat("result line with some detail. ", 60)
	})
	if !strings.Contains(out, report) {
		t.Fatalf("the report path was pushed out of the checkpoint:\n%s", out)
	}
	if !strings.Contains(out, "modifying tool calls") {
		t.Fatalf("the path must be filed as modified, got:\n%s", out)
	}
}

func TestLedgerKeepsWhatWasModifiedAndCapsEachSet(t *testing.T) {
	var l FileLedger
	l.noteToolCall("read_file", `{"path":"a.txt"}`)
	l.noteToolCall("edit", `{"path":"a.txt"}`)
	l.noteToolCall("read_file", `{"path":"a.txt"}`) // reading again must not demote it
	if len(l.Read) != 0 || len(l.Modified) != 1 || l.Modified[0] != "a.txt" {
		t.Fatalf("modified must win over read: %+v", l)
	}
	for i := 0; i < maxLedgerPaths+10; i++ {
		l.noteToolCall("read_file", fmt.Sprintf(`{"path":"f%03d"}`, i))
	}
	if len(l.Read) != maxLedgerPaths || l.Read[0] != "f010" || l.Read[len(l.Read)-1] != fmt.Sprintf("f%03d", maxLedgerPaths+9) {
		t.Fatalf("expected the newest %d, oldest first; got %d from %q to %q", maxLedgerPaths, len(l.Read), l.Read[0], l.Read[len(l.Read)-1])
	}
	l.noteToolCall("read_file", `{"path":"f020"}`) // seen again: moves to newest
	if l.Read[len(l.Read)-1] != "f020" || len(l.Read) != maxLedgerPaths {
		t.Fatalf("a re-seen path must become the newest: %q", l.Read[len(l.Read)-1])
	}
	seen := map[string]bool{}
	for _, p := range l.Read {
		if seen[p] {
			t.Fatalf("%q is listed twice", p)
		}
		seen[p] = true
	}
	if l.Read[0] != "f010" {
		t.Fatalf("moving a path must not evict an unrelated older one, first = %q", l.Read[0])
	}
}

func TestLedgerReadsPathsFromNestedAndListArguments(t *testing.T) {
	var l FileLedger
	l.noteToolCall("multiedit", `{"edits":[{"path":"x/a.go","old":"1","new":"2"},{"path":"x/b.go"}]}`)
	l.noteToolCall("view", `{"paths":["r/1.md","r/2.md"]}`)
	l.noteToolCall("view", `{"file_path":"r/3.md","relative_path":"r/4.md"}`)
	if got := strings.Join(l.Modified, ","); got != "x/a.go,x/b.go" && got != "x/b.go,x/a.go" {
		t.Errorf("modified = %v", l.Modified)
	}
	if len(l.Read) != 4 {
		t.Errorf("read = %v", l.Read)
	}
}

func TestLedgerIgnoresSearchRootsAndUnusableArguments(t *testing.T) {
	var l FileLedger
	l.noteToolCall("grep", `{"pattern":"x","path":"src"}`)
	l.noteToolCall("glob", `{"path":"src"}`)
	l.noteToolCall("ls", `{"path":"src"}`)
	l.noteToolCall("write", `not json at all`)
	l.noteToolCall("write", `{"content":"no path here"}`)
	l.noteToolCall("", `{"path":"x"}`)
	l.noteToolCall("write", `{"path":"`+strings.Repeat("p", 500)+`"}`)
	if !l.Empty() {
		t.Fatalf("nothing here names a file that was read or changed: %+v", l)
	}
}

// The model sees the built-in file tools as code_<name>; the ledger must treat
// them exactly as it treats the bare names.
func TestLedgerUnderstandsTheExposedCodeToolNames(t *testing.T) {
	var l FileLedger
	l.noteToolCall("code_grep", `{"pattern":"x","path":"src"}`)
	l.noteToolCall("code_glob", `{"path":"src"}`)
	l.noteToolCall("code_ls", `{"path":"src"}`)
	if !l.Empty() {
		t.Fatalf("search roots were filed as files: %+v", l)
	}
	l.noteToolCall("code_view", `{"file_path":"a.go"}`)
	l.noteToolCall("code_write", `{"file_path":"b.go","content":"x"}`)
	l.noteToolCall("code_edit", `{"file_path":"a.go"}`)
	l.noteToolCall("code_multiedit", `{"file_path":"c.go","edits":[]}`)
	want := []string{"a.go", "b.go", "c.go"}
	got := append([]string(nil), l.Modified...)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("modified = %v, want %v", l.Modified, want)
	}
	if len(l.Read) != 0 {
		t.Errorf("a file that was later changed is still listed as only read: %v", l.Read)
	}
}

func TestToolsDeclaredMutatingByTheAssemblyAreFiledAsModified(t *testing.T) {
	restore := SetFileMutationLookup(func(name string) bool { return name == "docx_replace" })
	var l FileLedger
	l.noteToolCall("docx_replace", `{"path":"D:\\合同\\a.docx"}`)
	l.noteToolCall("docx_outline", `{"path":"D:\\合同\\b.docx"}`)
	restore()
	l.noteToolCall("docx_replace", `{"path":"D:\\合同\\c.docx"}`) // lookup withdrawn
	if len(l.Modified) != 1 || l.Modified[0] != `D:\合同\a.docx` {
		t.Errorf("modified = %v", l.Modified)
	}
	if len(l.Read) != 2 {
		t.Errorf("read = %v", l.Read)
	}
}

func TestLedgerPromptKeepsWholePathsAndSaysWhatWasLeftOut(t *testing.T) {
	var l FileLedger
	for i := 0; i < 40; i++ {
		l.note(fmt.Sprintf(`C:\work\project\module%02d\handler.go`, i), true)
	}
	lines := l.promptLines(120)
	if len(lines) != 1 {
		t.Fatalf("lines = %v", lines)
	}
	text := lines[0]
	if !strings.Contains(text, `module39\handler.go`) {
		t.Errorf("the newest path must be kept: %s", text)
	}
	if !strings.Contains(text, "older omitted") {
		t.Errorf("omission must be stated: %s", text)
	}
	for _, part := range strings.Split(strings.SplitN(text, ": ", 2)[1], "; ") {
		if !strings.HasPrefix(part, `C:\work\project\module`) || !strings.HasSuffix(part, `\handler.go`) {
			t.Errorf("a path was cut: %q", part)
		}
	}
	if (&FileLedger{}).promptLines(1000) != nil || l.promptLines(10) != nil {
		t.Error("empty ledger or no budget must render nothing")
	}
}
