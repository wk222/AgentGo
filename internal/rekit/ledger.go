package rekit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Evidence is one claim in the case ledger. The ledger is what survives context resets:
// it is how an agent resumes a reverse job without re-deriving what was already proven.
type Evidence struct {
	ID         string   `json:"id"`
	Time       string   `json:"time"`
	Kind       string   `json:"kind"` // observation | derivation | inference | unknown
	Claim      string   `json:"claim"`
	Source     string   `json:"source,omitempty"`     // tool/file/offset/url that backs it
	Basis      []string `json:"basis,omitempty"`      // ids of the evidence this one is derived from
	Confidence string   `json:"confidence,omitempty"` // low|medium|high
	Gate       string   `json:"gate,omitempty"`       // objective check that passed/failed, e.g. "diff_exec 3205/3205"
	Tags       []string `json:"tags,omitempty"`
	Supersedes string   `json:"supersedes,omitempty"`
	NextStep   string   `json:"next_step,omitempty"` // mostly for unknowns
}

var ledgerMu sync.Mutex

func ledgerPath(home, c string) (string, error) {
	d, err := caseDir(home, c)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "evidence.jsonl"), nil
}

func readAll(p string) ([]Evidence, error) {
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Evidence
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var e Evidence
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// AddEvidence validates and appends one record, returning it with its assigned id.
func AddEvidence(home, c string, e Evidence) (*Evidence, error) {
	switch e.Kind {
	case KindObservation, KindDerivation, KindInference, KindUnknown:
	default:
		return nil, fmt.Errorf("kind must be observation|derivation|inference|unknown, got %q", e.Kind)
	}
	if strings.TrimSpace(e.Claim) == "" {
		return nil, fmt.Errorf("claim is required")
	}
	if e.Kind == KindObservation && e.Source == "" {
		return nil, fmt.Errorf("an observation needs a source (tool, file, offset or URL) so it can be re-checked")
	}
	if e.Kind == KindDerivation && len(e.Basis) == 0 {
		return nil, fmt.Errorf("a derivation needs basis evidence ids; if there are none it is an inference")
	}
	if e.Kind == KindInference && e.Confidence == "high" && e.Gate == "" {
		return nil, fmt.Errorf("an inference cannot be 'high' confidence without a gate (objective check); lower it or record the gate")
	}
	if e.Kind == KindInference && e.Confidence == "high" {
		if err := VerifyGateReference(home, c, e.Gate); err != nil {
			return nil, err
		}
	}
	p, err := ledgerPath(home, c)
	if err != nil {
		return nil, err
	}
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	all, err := readAll(p)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, x := range all {
		ids[x.ID] = true
	}
	for _, b := range append(append([]string{}, e.Basis...), e.Supersedes) {
		if b != "" && !ids[b] {
			return nil, fmt.Errorf("unknown evidence id %q referenced", b)
		}
	}
	e.ID = fmt.Sprintf("ev-%04d", len(all)+1)
	e.Time = time.Now().Format(time.RFC3339)
	line, _ := json.Marshal(e)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return nil, err
	}
	return &e, nil
}

// EvidenceQuery filters the ledger.
type EvidenceQuery struct {
	Kind, Tag, Text   string
	IncludeSuperseded bool
}

// ListEvidence returns matching records (superseded ones hidden by default).
func ListEvidence(home, c string, q EvidenceQuery) ([]Evidence, error) {
	p, err := ledgerPath(home, c)
	if err != nil {
		return nil, err
	}
	all, err := readAll(p)
	if err != nil {
		return nil, err
	}
	dead := map[string]bool{}
	for _, e := range all {
		if e.Supersedes != "" {
			dead[e.Supersedes] = true
		}
	}
	var out []Evidence
	for _, e := range all {
		if dead[e.ID] && !q.IncludeSuperseded {
			continue
		}
		if q.Kind != "" && e.Kind != q.Kind {
			continue
		}
		if q.Tag != "" && !contains(e.Tags, q.Tag) {
			continue
		}
		if q.Text != "" && !strings.Contains(strings.ToLower(e.Claim+" "+e.Source+" "+e.NextStep), strings.ToLower(q.Text)) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// RenderReport writes <case>/REPORT.md and returns (path, markdown). Sections follow the
// epistemic ladder, and open unknowns become the "next steps" list.
func RenderReport(home, c string) (string, string, error) {
	ev, err := ListEvidence(home, c, EvidenceQuery{})
	if err != nil {
		return "", "", err
	}
	byKind := map[string][]Evidence{}
	for _, e := range ev {
		byKind[e.Kind] = append(byKind[e.Kind], e)
	}
	var b strings.Builder
	name := c
	if name == "" {
		name = "default"
	}
	fmt.Fprintf(&b, "# 逆向案件报告：%s\n\n> 生成时间 %s ，共 %d 条有效证据（已被取代的不计入）。\n\n", name, time.Now().Format("2006-01-02 15:04"), len(ev))
	sections := []struct{ kind, title string }{
		{KindObservation, "一、观察（可复现的事实）"},
		{KindDerivation, "二、推导（由观察机械得出）"},
		{KindInference, "三、推断（未被门控证实，需要验证）"},
		{KindUnknown, "四、未知 / 下一步"},
	}
	for _, s := range sections {
		items := byKind[s.kind]
		fmt.Fprintf(&b, "## %s\n\n", s.title)
		if len(items) == 0 {
			b.WriteString("_（空）_\n\n")
			continue
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		for _, e := range items {
			fmt.Fprintf(&b, "- **%s** %s", e.ID, e.Claim)
			var meta []string
			if e.Source != "" {
				meta = append(meta, "来源: "+e.Source)
			}
			if len(e.Basis) > 0 {
				meta = append(meta, "依据: "+strings.Join(e.Basis, ","))
			}
			if e.Confidence != "" {
				meta = append(meta, "置信度: "+e.Confidence)
			}
			if e.Gate != "" {
				meta = append(meta, "门控: "+e.Gate)
			}
			if e.NextStep != "" {
				meta = append(meta, "下一步: "+e.NextStep)
			}
			if len(meta) > 0 {
				fmt.Fprintf(&b, "  \n  _%s_", strings.Join(meta, " | "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if gates, gerr := ListGates(home, c); gerr == nil && len(gates) > 0 {
		b.WriteString("## 五、客观门控核验记录（Gates）\n\n")
		for _, g := range gates {
			fmt.Fprintf(&b, "- **%s** `[%s]` (%s): %s\n", g.ID, g.Status, g.Kind, g.Summary)
		}
		b.WriteString("\n")
	}
	d, err := caseDir(home, c)
	if err != nil {
		return "", "", err
	}
	p := filepath.Join(d, "REPORT.md")
	return p, b.String(), os.WriteFile(p, []byte(b.String()), 0o644)
}
