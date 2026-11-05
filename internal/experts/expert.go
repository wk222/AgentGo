// Package experts is the library of experts: a role a conversation can run as.
//
// An expert is one Markdown file. A front matter block between two "---" lines
// gives its name, a one-line summary, the skills it relies on and a few starter
// prompts; the text after it is the rules the model follows for the whole
// conversation:
//
//	---
//	name: 财务建模师
//	summary: 把经营假设做成可调、可核对的测算模型。
//	category: office
//	skills: [office-financial-model, office-xlsx]
//	starters:
//	  - 帮我做一个三年期的现金流预测
//	---
//	（规则正文）
//
// Experts come from three places; the same id in a later one replaces the earlier:
// the ones inside the program, <data folder>/experts/*.md, and
// <workspace>/.agentgo/experts/*.md. The file name (without .md) is the id.
//
// An expert changes what the model is told, never what it is allowed to do:
// approvals, the sandbox and the tool governance apply as before.
package experts

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Source says where an expert came from.
type Source string

const (
	SourceOfficial  Source = "official"  // ships inside the program
	SourceUser      Source = "user"      // <data folder>/experts
	SourceWorkspace Source = "workspace" // <workspace>/.agentgo/experts
)

// Expert is one loaded expert.
type Expert struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Summary  string   `json:"summary"`
	Category string   `json:"category"`
	Skills   []string `json:"skills,omitempty"`   // skill names, without the "skill:" prefix
	Starters []string `json:"starters,omitempty"` // example first messages
	Order    int      `json:"order"`              // lower comes first inside a category
	Source   Source   `json:"source"`
	// Overrides is the source this expert replaced, when another one had the same id.
	Overrides Source `json:"overrides,omitempty"`
	// Path is the file of a user or workspace expert.
	Path string `json:"path,omitempty"`
	// Rules is the text the model follows. Lists leave it out; asking for one expert includes it.
	Rules string `json:"rules,omitempty"`
}

type frontMatter struct {
	Name     string   `yaml:"name"`
	Summary  string   `yaml:"summary"`
	Category string   `yaml:"category"`
	Skills   []string `yaml:"skills"`
	Starters []string `yaml:"starters"`
	Order    *int     `yaml:"order"`
}

var knownKeys = map[string]bool{"name": true, "summary": true, "category": true, "skills": true, "starters": true, "order": true}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// ValidID says whether s can be an expert id (and so a file name).
func ValidID(s string) bool { return idPattern.MatchString(s) }

const idRule = "只能用小写字母、数字和连字符，最多 48 个字符，并以字母或数字开头"

// Parse reads one expert file. id is the file name without ".md". The warnings
// are things that were ignored; an error means the file cannot be used.
func Parse(id, text string) (Expert, []string, error) {
	if !ValidID(id) {
		return Expert{}, nil, fmt.Errorf("文件名 %q 不能当专家 id：%s", id, idRule)
	}
	block, body, ok := splitFrontMatter(text)
	if !ok {
		return Expert{}, nil, errors.New("文件开头缺少 front matter：在两行 --- 之间写 name 和 summary，正文写规则")
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(block), &raw); err != nil {
		return Expert{}, nil, fmt.Errorf("front matter 不是合法的 YAML：%v", err)
	}
	var fm frontMatter
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		return Expert{}, nil, fmt.Errorf("front matter 的字段类型不对（skills、starters 要写成列表，order 要写数字）：%v", err)
	}
	var warnings []string
	var unknown []string
	for k := range raw {
		if !knownKeys[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		warnings = append(warnings, "front matter 里有不认识的键，已忽略："+strings.Join(unknown, "、"))
	}

	e := Expert{ID: id, Order: defaultOrder, Rules: strings.TrimSpace(body)}
	if fm.Order != nil {
		e.Order = *fm.Order
	}
	var err error
	if e.Name, err = oneLine("name", fm.Name, MaxNameRunes, true); err != nil {
		return Expert{}, nil, err
	}
	if e.Summary, err = oneLine("summary", fm.Summary, MaxSummaryRunes, true); err != nil {
		return Expert{}, nil, err
	}
	if e.Category, err = oneLine("category", strings.ToLower(fm.Category), MaxCategoryRune, false); err != nil {
		return Expert{}, nil, err
	}
	if e.Category == "" {
		e.Category = "general"
	}
	if e.Skills, err = cleanSkills(fm.Skills); err != nil {
		return Expert{}, nil, err
	}
	if len(fm.Starters) > MaxStarters {
		return Expert{}, nil, fmt.Errorf("starters 有 %d 条，最多 %d 条", len(fm.Starters), MaxStarters)
	}
	for _, s := range fm.Starters {
		s, err = oneLine("starters 里的一条", s, MaxStarterRunes, false)
		if err != nil {
			return Expert{}, nil, err
		}
		if s != "" {
			e.Starters = append(e.Starters, s)
		}
	}
	if n := utf8.RuneCountInString(e.Rules); n < MinRulesRunes {
		return Expert{}, nil, fmt.Errorf("规则正文太短（%d 字）：front matter 之后要写出这个专家该怎么工作，至少 %d 字", n, MinRulesRunes)
	}
	if len(e.Rules) > MaxRulesBytes {
		return Expert{}, nil, fmt.Errorf("规则正文 %d 字节，超过上限 %d：它会进每一次模型调用的系统提示，请精简", len(e.Rules), MaxRulesBytes)
	}
	return e, warnings, nil
}

// oneLine trims s and checks it is a single line within max runes.
func oneLine(field, s string, max int, required bool) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		if required {
			return "", fmt.Errorf("缺少 %s", field)
		}
		return "", nil
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", fmt.Errorf("%s 必须是单行文字", field)
	}
	if n := utf8.RuneCountInString(s); n > max {
		return "", fmt.Errorf("%s 有 %d 字，最多 %d 字", field, n, max)
	}
	return s, nil
}

// cleanSkills trims the names, drops a "skill:" prefix and repeats.
func cleanSkills(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "skill:"))
		if s == "" || seen[s] {
			continue
		}
		if strings.ContainsAny(s, "\r\n") || utf8.RuneCountInString(s) > MaxSkillRunes {
			return nil, fmt.Errorf("skills 里的 %q 不是技能名（单行，最多 %d 字）", clip(s, 20), MaxSkillRunes)
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) > MaxSkills {
		return nil, fmt.Errorf("skills 有 %d 个，最多 %d 个", len(out), MaxSkills)
	}
	return out, nil
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// splitFrontMatter returns the block between the leading "---" line and the next
// "---" line, and the text after it. A BOM and CRLF line ends are tolerated.
func splitFrontMatter(text string) (block, body string, ok bool) {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", "", false
	}
	rest := text[len("---\n"):]
	for off := 0; off <= len(rest); {
		nl := strings.IndexByte(rest[off:], '\n')
		line, next := rest[off:], len(rest)+1
		if nl >= 0 {
			line, next = rest[off:off+nl], off+nl+1
		}
		if strings.TrimRight(line, " \t") == "---" {
			if next > len(rest) {
				return rest[:off], "", true
			}
			return rest[:off], rest[next:], true
		}
		if nl < 0 {
			break
		}
		off = next
	}
	return "", "", false
}

// Prompt renders the block added to the system prompt of a conversation that
// runs as this expert. has says whether a skill exists right now; only skills
// that do are named, so the model is never sent after one that is missing. A nil
// has names them all.
func (e Expert) Prompt(has func(skill string) bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Expert: %s (%s)]\n", e.Name, e.ID)
	b.WriteString("Work as this expert for the whole conversation. Its rules add to the safety, permission and approval rules above and never replace them; where they conflict, those win.\n")
	fmt.Fprintf(&b, "Summary: %s\n", e.Summary)
	b.WriteString("Rules:\n")
	b.WriteString(strings.TrimSpace(e.Rules))
	var named []string
	for _, s := range e.Skills {
		if has == nil || has(s) {
			named = append(named, "skill:"+s)
		}
	}
	if len(named) > 0 {
		b.WriteString("\n\nSkills this expert relies on (load one with activate_skill when the task needs it; do not write its content from memory): ")
		b.WriteString(strings.Join(named, ", "))
	}
	return b.String()
}
