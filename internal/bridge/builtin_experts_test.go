package bridge

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"agentgo/internal/config"
	"agentgo/internal/experts"
	"agentgo/internal/skills"
)

// The official experts are prose too: their rules tell the model which tool to
// call and which skill to load. This checks every `snake_case` word, skill and
// AGENTGO_* name in them against the running program, the same way the built-in
// skills are checked, so a renamed tool or a dropped skill fails here instead of
// quietly sending a conversation after something that is not there.

var expertSkillRef = regexp.MustCompile(`skill:([a-z0-9][a-z0-9-]*)`)

func TestOfficialExpertsNameOnlyWhatTheProgramHas(t *testing.T) {
	words := runtimeVocabulary(t)
	have := map[string]bool{}
	for _, s := range skills.NewLoader(t.TempDir()).Reload() {
		have[s.Name] = true
	}

	// Under the default switches every official expert is on offer.
	var official []experts.Expert
	for _, e := range experts.Load(experts.Options{}).Experts {
		if e.Source == experts.SourceOfficial {
			official = append(official, e)
		}
	}
	if want := len(experts.OfficialIDs()); len(official) != want {
		t.Fatalf("%d official experts are offered by default, %d exist: one is gated off or does not parse", len(official), want)
	}

	for _, e := range official {
		var unknown []string
		for _, m := range skillSnakeWord.FindAllStringSubmatch(e.Rules, -1) {
			if !words[m[1]] {
				unknown = append(unknown, m[1])
			}
		}
		for _, name := range skillFlagWord.FindAllString(e.Rules, -1) {
			if !config.Declared(name) {
				unknown = append(unknown, name)
			}
		}
		for _, m := range expertSkillRef.FindAllStringSubmatch(e.Rules, -1) {
			if !have[m[1]] {
				unknown = append(unknown, "skill:"+m[1])
			}
		}
		for _, name := range e.Skills {
			if !have[name] {
				unknown = append(unknown, "skill:"+name+" (front matter)")
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			t.Errorf("expert %s names things the program does not have: %s", e.ID, strings.Join(unknown, ", "))
		}
	}
}
