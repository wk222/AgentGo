package experts

import (
	"embed"
	"fmt"
	"sort"
	"strings"
	"sync"

	"agentgo/internal/config"
)

// The experts that come with the program are Markdown files in builtin/, in the
// same format as a user's own. They are written in Chinese like the built-in
// skills; the model answers in the user's language either way.
//
//go:embed builtin/*.md
var builtinFS embed.FS

// builtinGates hides an official expert whose tools the user switched off, the
// same way the built-in skills are hidden: an expert that sends the model after
// tools it does not have is worse than none. An id not listed is always shown.
var builtinGates = map[string]func() bool{
	"office-assistant":    config.OfficeDoc.On,
	"data-analyst":        config.OfficeDoc.On,
	"dashboard-builder":   config.OfficeDoc.On,
	"financial-modeler":   config.OfficeDoc.On,
	"pitch-deck-designer": config.OfficeDoc.On,
	"paper-writer":        config.OfficeDoc.On,
	"document-analyst":    config.DocIndex.On,
	"web-operator":        config.BrowserControl.On,
	"software-engineer":   config.CodeTools.On,
}

// builtinNeeds names a tool an official expert cannot work without. A switch is
// not always enough to know the tool is there (the browser tools need a browser
// installed), so a host that can tell passes Options.HasTool and the expert is
// left out when the tool is missing.
var builtinNeeds = map[string]string{
	"office-assistant":    "read_xlsx",
	"data-analyst":        "read_xlsx",
	"dashboard-builder":   "edit_xlsx",
	"financial-modeler":   "edit_xlsx",
	"pitch-deck-designer": "inspect_pptx",
	"paper-writer":        "create_docx",
	"document-analyst":    "search_documents",
	"web-operator":        "browser_open",
	"software-engineer":   "code_view",
}

var (
	parseOnce  sync.Once
	parsedAll  []Expert
	parsedWarn []string
)

// officialExperts returns the experts inside the program that are switched on and,
// when hasTool is given, whose key tool exists. The files are parsed once; the
// switches are read on every call.
func officialExperts(hasTool func(string) bool) ([]Expert, []string) {
	parseOnce.Do(func() {
		files, err := builtinFS.ReadDir("builtin")
		if err != nil {
			parsedWarn = append(parsedWarn, fmt.Sprintf("读不了内置专家：%v", err))
			return
		}
		for _, f := range files {
			name := f.Name()
			if f.IsDir() || !strings.HasSuffix(name, ".md") {
				continue
			}
			data, err := builtinFS.ReadFile("builtin/" + name)
			if err != nil {
				parsedWarn = append(parsedWarn, fmt.Sprintf("内置专家 %s：%v", name, err))
				continue
			}
			e, notes, err := Parse(strings.TrimSuffix(name, ".md"), string(data))
			for _, n := range notes {
				parsedWarn = append(parsedWarn, fmt.Sprintf("内置专家 %s：%s", name, n))
			}
			if err != nil {
				parsedWarn = append(parsedWarn, fmt.Sprintf("内置专家 %s：%v", name, err))
				continue
			}
			e.Source = SourceOfficial
			parsedAll = append(parsedAll, e)
		}
		sort.Slice(parsedAll, func(i, j int) bool { return parsedAll[i].ID < parsedAll[j].ID })
	})
	var out []Expert
	for _, e := range parsedAll {
		if gate, ok := builtinGates[e.ID]; ok && !gate() {
			continue
		}
		if tool, ok := builtinNeeds[e.ID]; ok && hasTool != nil && !hasTool(tool) {
			continue
		}
		out = append(out, e)
	}
	return out, parsedWarn
}

// OfficialIDs lists the ids of every expert inside the program, whatever the
// switches say. Tests use it to check the files against the running program.
func OfficialIDs() []string {
	officialExperts(nil)
	ids := make([]string, 0, len(parsedAll))
	for _, e := range parsedAll {
		ids = append(ids, e.ID)
	}
	return ids
}
