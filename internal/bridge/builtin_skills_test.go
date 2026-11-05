package bridge

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"

	"agentgo/internal/browsercontrol"
	"agentgo/internal/config"
	"agentgo/internal/skills"
)

// The built-in skills are prose. If a tool or an option is renamed, they would go
// on telling the model to call something that no longer exists, and nothing else
// would notice. This reads every `snake_case` word and AGENTGO_* name in them and
// checks it against the running program: the tools of a full runtime (office and
// document tools included), the options in their input schemas, the words the
// tools' own descriptions use for values (op names, layouts, calculations), and the
// declared switches.

var (
	skillSnakeWord = regexp.MustCompile("`([a-z][a-z0-9]*(?:_[a-z0-9]+)+)`")
	bareSnakeWord  = regexp.MustCompile(`[a-z][a-z0-9]*(?:_[a-z0-9]+)+`)
	skillFlagWord  = regexp.MustCompile(`AGENTGO_[A-Z0-9_]+`)
)

// schemaWords adds what a JSON schema of a tool input offers the model: property
// names, enumerated values, and the snake_case words its descriptions use for the
// values an option takes (a description such as "set_cell | add_chart | ..." is how
// many options list theirs).
func schemaWords(node any, into map[string]bool) {
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			switch key {
			case "properties":
				if props, ok := child.(map[string]any); ok {
					for name := range props {
						into[name] = true
					}
				}
			case "enum":
				if values, ok := child.([]any); ok {
					for _, e := range values {
						if s, ok := e.(string); ok {
							into[s] = true
						}
					}
				}
			case "description":
				if s, ok := child.(string); ok {
					for _, w := range bareSnakeWord.FindAllString(s, -1) {
						into[w] = true
					}
				}
			}
			schemaWords(child, into)
		}
	case []any:
		for _, child := range v {
			schemaWords(child, into)
		}
	}
}

// stricterElsewhere are the skills internal/officedoc checks against the office
// tools in both directions, output fields included.
var stricterElsewhere = map[string]bool{
	"office": true, "office-xlsx": true, "office-pptx": true, "office-docx": true, "office-report": true,
}

// runtimeVocabulary is every snake_case word that names something the program has:
// the tools of a full runtime (office and document tools included), the options in
// their input schemas, the words their descriptions use for values, and the few
// that exist only behind a switch or in what a tool returns. Prose that tells the
// model what to call (built-in skills, official experts) is checked against it.
func runtimeVocabulary(t *testing.T) map[string]bool {
	t.Helper()
	rt := boot(t)
	app := NewAppService(rt)
	t.Cleanup(app.Close)

	words := map[string]bool{}
	tools := 0
	for _, bt := range rt.toolReg.GetAllTools() {
		info, err := bt.Info(context.Background())
		if err != nil || info == nil {
			t.Fatalf("tool info: %v", err)
		}
		tools++
		words[info.Name] = true
		for _, w := range bareSnakeWord.FindAllString(info.Desc, -1) {
			words[w] = true
		}
		if info.ParamsOneOf == nil {
			continue
		}
		sc, err := info.ParamsOneOf.ToJSONSchema()
		if err != nil {
			t.Fatalf("%s: %v", info.Name, err)
		}
		raw, _ := json.Marshal(sc)
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		schemaWords(doc, words)
	}
	for _, must := range []string{"create_xlsx", "read_xlsx", "edit_xlsx", "analyze_xlsx", "create_pptx", "inspect_pptx", "create_docx",
		"index_documents", "search_documents", "read_document_neighbors", "parse_file", "manage_mcp_server", "evolve_skill", "run_skill_script"} {
		if !words[must] {
			t.Fatalf("the runtime lacks the tool %s (%d tools): the check below would be vacuous", must, tools)
		}
	}

	// Words the skills may use that no input schema or tool description holds: tools
	// that exist only behind a switch or are added by the agent loop, and fields that
	// tools return rather than take.
	for _, w := range []string{
		"tool_search",               // added to the agent by the tool-search middleware
		"computer_use", "ocr_image", // only registered when their switch is on
		"next_offset", "weak_match", "read_more", // fields of parse_file and search_documents results
		// Only registered when a browser is installed, so absent on a machine without one.
		browsercontrol.ToolOpen, browsercontrol.ToolSnapshot, browsercontrol.ToolClick, browsercontrol.ToolFill,
		browsercontrol.ToolPress, browsercontrol.ToolScroll, browsercontrol.ToolBack, browsercontrol.ToolScreenshot,
		browsercontrol.ToolClose,
	} {
		words[w] = true
	}
	return words
}

func TestBuiltinSkillsNameOnlyWhatTheProgramHas(t *testing.T) {
	words := runtimeVocabulary(t)
	// Variables handed to a skill's script, not settings of the program.
	scriptEnv := map[string]bool{"AGENTGO_SKILL_DIR": true, "AGENTGO_SKILL_OUTPUT_DIR": true}

	checked := 0
	for _, s := range skills.NewLoader(t.TempDir()).Reload() {
		if s.Scope != skills.ScopeBuiltin {
			continue
		}
		checked++
		var unknown []string
		if !stricterElsewhere[s.Name] {
			for _, m := range skillSnakeWord.FindAllStringSubmatch(s.Content, -1) {
				if !words[m[1]] {
					unknown = append(unknown, m[1])
				}
			}
		}
		for _, name := range skillFlagWord.FindAllString(s.Content, -1) {
			if !config.Declared(name) && !scriptEnv[name] {
				unknown = append(unknown, name)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			t.Errorf("skill %s names things the program does not have: %s", s.Name, strings.Join(unknown, ", "))
		}
	}
	if checked < 13 {
		t.Errorf("only %d built-in skills were checked", checked)
	}
}
