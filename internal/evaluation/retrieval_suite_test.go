package evaluation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func writeBytes(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func writeFile(path, content string) error { return writeBytes(path, []byte(content)) }

func gbk(t *testing.T, s string) []byte {
	t.Helper()
	b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("encode %q as GBK: %v", s, err)
	}
	return b
}

func mustWrite(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := writeBytes(path, content); err != nil {
		t.Fatal(err)
	}
}

const inlineSuite = `{
  "docs": [
    {"id": "a.md", "text": "Taxi rides are refunded within thirty days."},
    {"id": "b.md", "text": "Hotel receipts must show the guest name."}
  ],
  "cases": [
    {"query": "taxi refund", "relevant": ["a.md"]},
    {"id": "hotel", "query": "hotel receipt", "relevant": ["b.md", "a.md"]}
  ]
}`

func TestLoadSuiteWithInlineDocs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "travel-rules.json")
	mustWrite(t, path, []byte(inlineSuite))
	s, err := LoadRetrievalSuite(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "travel-rules" {
		t.Errorf("Name = %q, want the file name without extension", s.Name)
	}
	if len(s.Docs) != 2 || len(s.Cases) != 2 {
		t.Fatalf("docs=%d cases=%d", len(s.Docs), len(s.Cases))
	}
	if s.Cases[0].ID != "q1" || s.Cases[1].ID != "hotel" {
		t.Errorf("case IDs = %q, %q; want q1 filled in and hotel kept", s.Cases[0].ID, s.Cases[1].ID)
	}
}

func TestLoadSuiteReadsADocumentFolder(t *testing.T) {
	dir := t.TempDir()
	taxi := "出租车费用报销规则：员工因公乘坐出租车，需保留发票，三十日内提交报销申请。"
	invoice := "发票报销需要提供增值税专用发票，金额超过五千元的需要部门经理审批。"
	faq := "常见问题：发票抬头必须与公司全称一致，否则财务部不予受理。"
	corpus := filepath.Join(dir, "corpus")
	mustWrite(t, filepath.Join(corpus, "travel", "taxi.md"), []byte(taxi))
	mustWrite(t, filepath.Join(corpus, "finance", "invoice.txt"), gbk(t, invoice+invoice))
	mustWrite(t, filepath.Join(corpus, "finance", "faq.txt"), append([]byte{0xEF, 0xBB, 0xBF}, faq...))
	mustWrite(t, filepath.Join(corpus, "UPPER.MD"), []byte("upper case extension"))
	mustWrite(t, filepath.Join(corpus, "logo.png"), []byte{0x89, 'P', 'N', 'G', 0})
	mustWrite(t, filepath.Join(corpus, "blank.md"), []byte("  \n\n"))
	mustWrite(t, filepath.Join(dir, "suite.json"), []byte(`{"docs_dir": "corpus", "cases": [
		{"query": "taxi", "relevant": ["travel/taxi.md"]},
		{"query": "invoice", "relevant": ["finance/invoice.txt", "finance/faq.txt"]}]}`))

	s, err := LoadRetrievalSuite(filepath.Join(dir, "suite.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	text := map[string]string{}
	for _, d := range s.Docs {
		ids = append(ids, d.ID)
		text[d.ID] = d.Text
	}
	if got, want := strings.Join(ids, ","), "UPPER.MD,finance/faq.txt,finance/invoice.txt,travel/taxi.md"; got != want {
		t.Errorf("document IDs = %s\nwant            %s\n(forward slashes, sorted; png and blank file left out)", got, want)
	}
	if text["travel/taxi.md"] != taxi {
		t.Errorf("UTF-8 document came out as %q", text["travel/taxi.md"])
	}
	if text["finance/invoice.txt"] != invoice+invoice {
		t.Errorf("GBK document came out as %q", text["finance/invoice.txt"])
	}
	if text["finance/faq.txt"] != faq {
		t.Errorf("document with a byte order mark came out as %q", text["finance/faq.txt"])
	}

	// The same folder named by an absolute path.
	mustWrite(t, filepath.Join(dir, "abs.json"), []byte(`{"docs_dir": `+quote(corpus)+`, "cases": [{"query": "taxi", "relevant": ["travel/taxi.md"]}]}`))
	if _, err := LoadRetrievalSuite(filepath.Join(dir, "abs.json")); err != nil {
		t.Errorf("absolute docs_dir: %v", err)
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

func TestLoadSuiteFileSavedByNotepad(t *testing.T) {
	body := `{"docs": [{"id": "a.md", "text": "报销流程说明"}], "cases": [{"query": "怎么报销", "relevant": ["a.md"]}]}`
	for name, raw := range map[string][]byte{
		"gbk":    gbk(t, body),
		"utf8":   []byte(body),
		"bom":    append([]byte{0xEF, 0xBB, 0xBF}, body...),
		"utf-16": utf16le(body),
	} {
		path := filepath.Join(t.TempDir(), "s.json")
		mustWrite(t, path, raw)
		s, err := LoadRetrievalSuite(path)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if s.Cases[0].Query != "怎么报销" || s.Docs[0].Text != "报销流程说明" {
			t.Errorf("%s: read %q / %q", name, s.Cases[0].Query, s.Docs[0].Text)
		}
	}
}

func utf16le(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8)) // every character of the test text is in the BMP
	}
	return out
}

func TestLoadSuiteRejectsMistakes(t *testing.T) {
	docs := `"docs": [{"id": "a.md", "text": "alpha"}, {"id": "dir/b.md", "text": "beta"}]`
	tests := []struct {
		name, json, want string
	}{
		{"misspelt field", `{` + docs + `, "cases": [{"query": "q", "relevent": ["a.md"]}]}`, "unknown field"},
		{"not JSON", `{"docs": `, "unexpected"},
		{"no documents", `{"cases": [{"query": "q", "relevant": ["a.md"]}]}`, "no documents"},
		{"duplicate document", `{"docs": [{"id": "a", "text": "x"}, {"id": "a", "text": "y"}], "cases": [{"query": "q", "relevant": ["a"]}]}`, `duplicate document id "a"`},
		{"empty document", `{"docs": [{"id": "a", "text": " "}], "cases": [{"query": "q", "relevant": ["a"]}]}`, "is empty"},
		{"document without id", `{"docs": [{"text": "x"}], "cases": [{"query": "q", "relevant": ["a"]}]}`, "has no id"},
		{"no cases", `{` + docs + `, "cases": []}`, "no cases"},
		{"empty query", `{` + docs + `, "cases": [{"query": " ", "relevant": ["a.md"]}]}`, "empty query"},
		{"nothing relevant", `{` + docs + `, "cases": [{"query": "q", "relevant": []}]}`, "no relevant document"},
		{"unknown relevant document", `{` + docs + `, "cases": [{"query": "q", "relevant": ["zzz.md"]}]}`, `"zzz.md" is not in the corpus`},
		{"backslash path", `{` + docs + `, "cases": [{"query": "q", "relevant": ["dir\\b.md"]}]}`, "forward slashes"},
		{"relevant twice", `{` + docs + `, "cases": [{"query": "q", "relevant": ["a.md", "a.md"]}]}`, "twice"},
		{"explicit id collides with a generated one", `{` + docs + `, "cases": [{"id": "q2", "query": "x", "relevant": ["a.md"]}, {"query": "y", "relevant": ["a.md"]}]}`, `duplicate case id "q2"`},
		{"docs and docs_dir", `{` + docs + `, "docs_dir": ".", "cases": [{"query": "q", "relevant": ["a.md"]}]}`, "either docs or docs_dir"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.json")
			mustWrite(t, path, []byte(tc.json))
			_, err := LoadRetrievalSuite(path)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadCorpusDirRefusesBinaryFilesAndEmptyFolders(t *testing.T) {
	empty := t.TempDir()
	mustWrite(t, filepath.Join(empty, "logo.png"), []byte{0x89, 'P', 'N', 'G'})
	if _, err := LoadCorpusDir(empty); err == nil || !strings.Contains(err.Error(), "no .md") {
		t.Errorf("folder without documents: err = %v", err)
	}

	bin := t.TempDir()
	mustWrite(t, filepath.Join(bin, "scan.txt"), []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	if _, err := LoadCorpusDir(bin); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Errorf("binary file named .txt: err = %v; it must not be indexed as text", err)
	}

	if _, err := LoadCorpusDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing folder: want an error")
	}
}
