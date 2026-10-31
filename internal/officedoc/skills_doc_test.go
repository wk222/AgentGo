package officedoc

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The built-in office skills (internal/skills/builtin/office*) tell the model
// how to call these tools. This test keeps the two from drifting apart: every
// option the tools accept is described, and nothing the tools lack is promised.

func skillText(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "skills", "builtin", name, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// jsonNames collects the JSON field names of a struct type and everything it holds.
func jsonNames(t reflect.Type, into map[string]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
			into[name] = true
		}
		jsonNames(f.Type, into)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var snakeCode = regexp.MustCompile("`([a-z][a-z0-9]*(?:_[a-z0-9]+)+)`")

func TestOfficeSkillsMatchTheTools(t *testing.T) {
	ts, err := Tools(Env{Resolve: func(string) (string, error) { return "", nil }})
	if err != nil {
		t.Fatal(err)
	}
	var toolNames []string
	for _, bt := range ts {
		info, _ := bt.Info(context.Background())
		toolNames = append(toolNames, info.Name)
	}

	// What each skill may name: tool names, option names, enumerated values.
	vocab := map[string]map[string]bool{}
	add := func(skill string, words ...string) {
		if vocab[skill] == nil {
			vocab[skill] = map[string]bool{"activate_skill": true}
		}
		for _, w := range words {
			vocab[skill][w] = true
		}
	}
	add("office", toolNames...)
	add("office-docx", toolNames...)
	add("office-xlsx", toolNames...)
	add("office-pptx", toolNames...)
	add("office-report", toolNames...)

	var xlsxOps []string
	opDesc := reflect.TypeOf(xlsxOp{})
	if f, ok := opDesc.FieldByName("Op"); ok {
		desc := strings.TrimPrefix(f.Tag.Get("jsonschema"), "required,description=")
		for _, w := range strings.Split(desc, "|") {
			xlsxOps = append(xlsxOps, strings.TrimSpace(w))
		}
	}
	if len(xlsxOps) < 10 {
		t.Fatalf("could not read the op names from the schema: %v", xlsxOps)
	}
	for _, typ := range []any{createXlsxInput{}, readXlsxInput{}, editXlsxInput{}, xlsxOp{}, xlsxFormat{}, xlsxChart{}} {
		names := map[string]bool{}
		jsonNames(reflect.TypeOf(typ), names)
		add("office-xlsx", keys(names)...)
	}
	add("office-xlsx", xlsxOps...)
	add("office-xlsx", ToolAnalyzeXlsx)
	add("office-xlsx", keys(chartKinds)...)

	for _, typ := range []any{createPptxInput{}, pptxSlideIn{}, inspectPptxInput{}, tplInfo{}} {
		names := map[string]bool{}
		jsonNames(reflect.TypeOf(typ), names)
		add("office-pptx", keys(names)...)
	}
	analyzeNames, analyzeIn := map[string]bool{}, map[string]bool{}
	for _, typ := range []any{analyzeXlsxInput{}, analyzeXlsxOutput{}} {
		jsonNames(reflect.TypeOf(typ), analyzeNames)
	}
	jsonNames(reflect.TypeOf(analyzeXlsxInput{}), analyzeIn)
	add("office-report", keys(analyzeNames)...)
	add("office-report", keys(fnAlias)...)
	add("office-report", keys(condOps)...)
	add("office-report", keys(opAlias)...)
	add("office-report", "change_pct", "keep_total_rows", "save_as", "conditional_format", "to_number")
	layouts := []string{"bullets", "two_column", "table", "chart", "stats", "timeline", "image", "section", "svg"}
	add("office-pptx", layouts...)
	add("office-pptx", keys(pptxChartKinds)...)

	docxNames := map[string]bool{}
	jsonNames(reflect.TypeOf(createDocxInput{}), docxNames)
	add("office-docx", keys(docxNames)...)

	// 1. Nothing is promised that the tools do not have.
	for skill, words := range vocab {
		text := skillText(t, skill)
		for _, m := range snakeCode.FindAllStringSubmatch(text, -1) {
			if !words[m[1]] {
				t.Errorf("%s: `%s` is not a tool, option or value of the tools it describes", skill, m[1])
			}
		}
	}

	// 2. Everything the tools accept is described.
	mustMention := func(skill string, words ...string) {
		text := skillText(t, skill)
		for _, w := range words {
			if !strings.Contains(text, "`"+w+"`") {
				t.Errorf("%s does not describe `%s`", skill, w)
			}
		}
	}
	mustMention("office", toolNames...)
	mustMention("office-xlsx", ToolCreateXlsx, ToolReadXlsx, ToolAnalyzeXlsx, ToolEditXlsx)
	// Every option of analyze_xlsx, and the calculations it offers, are described.
	mustMention("office-report", ToolAnalyzeXlsx, ToolReadXlsx, ToolCreateDocx)
	mustMention("office-report", keys(analyzeIn)...)
	mustMention("office-report", "sum", "avg", "min", "max", "median", "count", "count_distinct", "first", "last", "weighted_avg")
	mustMention("office-report", "profile", "aggregate", "compare", "pivot", "rows", "in", "contains", "blank", "notblank")
	mustMention("office-xlsx", keys(chartKinds)...)
	for _, op := range xlsxOps {
		// Related ops share a table row ("add_sheet / rename_sheet"), so look for the bare name.
		if !strings.Contains(skillText(t, "office-xlsx"), op) {
			t.Errorf("office-xlsx does not describe the op %s", op)
		}
	}
	mustMention("office-pptx", ToolCreatePptx, ToolInspectPptx)
	mustMention("office-pptx", layouts...)
	mustMention("office-pptx", keys(pptxChartKinds)...)
	mustMention("office-pptx", keys(palettes)...)
	mustMention("office-pptx", "template", "layouts", "cover_layout", "content_layout", "closing_layout", "template_layout")
	mustMention("office-docx", ToolCreateDocx)

	// 3. The layouts, chart types and themes of the skill are exactly the real ones.
	if _, err := createPptx(Env{Resolve: func(string) (string, error) { return filepath.Join(t.TempDir(), "x.pptx"), nil }},
		createPptxInput{RelativePath: "x", Title: "t", Slides: []pptxSlideIn{{Layout: "nope", Title: "x"}}}); err == nil ||
		!strings.Contains(err.Error(), strings.Join(layouts, ", ")) {
		t.Errorf("the layouts of the skill differ from the ones the tool lists: %v", err)
	}
}
