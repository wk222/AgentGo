package rekit

import (
	"bufio"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Gate kinds supported by re_gate.
const (
	GateHashEq      = "hash_eq"
	GateFormatValid = "format_valid"
	GateByteParity  = "byte_parity"
	GateJSONGolden  = "json_golden"
)

// GateStatus values.
const (
	GatePass = "PASS"
	GateFail = "FAIL"
)

// GateRequest describes an objective verification check to execute and persist.
type GateRequest struct {
	Case         string  `json:"case,omitempty"`
	Kind         string  `json:"kind"`                     // hash_eq | format_valid | byte_parity | json_golden
	Path         string  `json:"path,omitempty"`           // primary artifact path
	OtherPath    string  `json:"other_path,omitempty"`     // reference / golden artifact path
	Expected     string  `json:"expected,omitempty"`       // expected hash hex or expected format kind
	Algo         string  `json:"algo,omitempty"`           // sha256 (default) | sha1 | md5
	MaxDiffBytes int64   `json:"max_diff_bytes,omitempty"` // tolerance for byte_parity (default 0)
	AbsTol       float64 `json:"abs_tol,omitempty"`        // numeric tolerance for json_golden (default 1e-6)
	Note         string  `json:"note,omitempty"`
}

// GateReceipt is the persisted record of a gate execution in <case>/gates.jsonl.
type GateReceipt struct {
	ID      string         `json:"id"`
	Time    string         `json:"time"`
	Kind    string         `json:"kind"`
	Status  string         `json:"status"` // PASS | FAIL
	Summary string         `json:"summary"`
	Path    string         `json:"path,omitempty"`
	Metrics map[string]any `json:"metrics,omitempty"`
	Note    string         `json:"note,omitempty"`
}

var (
	gateMu   sync.Mutex
	gateIDRe = regexp.MustCompile(`\bgate-\d{4,}\b`)
)

func gatesPath(home, c string) (string, error) {
	d, err := caseDir(home, c)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "gates.jsonl"), nil
}

func readAllGates(p string) ([]GateReceipt, error) {
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []GateReceipt
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var g GateReceipt
		if json.Unmarshal(sc.Bytes(), &g) == nil && g.ID != "" {
			out = append(out, g)
		}
	}
	return out, sc.Err()
}

// ListGates returns all gate receipts recorded for the case.
func ListGates(home, c string) ([]GateReceipt, error) {
	p, err := gatesPath(home, c)
	if err != nil {
		return nil, err
	}
	gateMu.Lock()
	defer gateMu.Unlock()
	return readAllGates(p)
}

// VerifyGateReference checks any gate-XXXX IDs mentioned in gateStr.
// If gateStr references one or more gate IDs, every referenced gate must exist in the
// case and have Status == PASS.
func VerifyGateReference(home, c, gateStr string) error {
	refs := gateIDRe.FindAllString(gateStr, -1)
	if len(refs) == 0 {
		return nil
	}
	all, err := ListGates(home, c)
	if err != nil {
		return err
	}
	byID := make(map[string]GateReceipt, len(all))
	for _, g := range all {
		byID[g.ID] = g
	}
	for _, ref := range refs {
		g, ok := byID[ref]
		if !ok {
			return fmt.Errorf("referenced gate %q does not exist in case %q; run re_gate first", ref, c)
		}
		if g.Status != GatePass {
			return fmt.Errorf("referenced gate %q has status %s (%s); high-confidence inference requires a PASS gate", ref, g.Status, g.Summary)
		}
	}
	return nil
}

// RunGate executes an objective check, persists the receipt to <case>/gates.jsonl, and returns it.
func RunGate(home, c string, req GateRequest) (*GateReceipt, error) {
	if strings.TrimSpace(req.Path) == "" {
		return nil, fmt.Errorf("path is required")
	}
	absPrimary, err := absPath(req.Path)
	if err != nil {
		return nil, err
	}
	req.Path = absPrimary
	if strings.TrimSpace(req.OtherPath) != "" {
		absOther, err := absPath(req.OtherPath)
		if err != nil {
			return nil, err
		}
		req.OtherPath = absOther
	}

	var rec GateReceipt
	rec.Kind = strings.ToLower(strings.TrimSpace(req.Kind))
	rec.Path = req.Path
	rec.Note = strings.TrimSpace(req.Note)

	switch rec.Kind {
	case GateHashEq:
		if err := evalHashEq(&rec, req); err != nil {
			return nil, err
		}
	case GateFormatValid:
		if err := evalFormatValid(&rec, req); err != nil {
			return nil, err
		}
	case GateByteParity:
		if err := evalByteParity(&rec, req); err != nil {
			return nil, err
		}
	case GateJSONGolden:
		if err := evalJSONGolden(&rec, req); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown gate kind %q (want hash_eq|format_valid|byte_parity|json_golden)", req.Kind)
	}

	gp, err := gatesPath(home, c)
	if err != nil {
		return nil, err
	}
	gateMu.Lock()
	defer gateMu.Unlock()
	all, err := readAllGates(gp)
	if err != nil {
		return nil, err
	}
	rec.ID = fmt.Sprintf("gate-%04d", len(all)+1)
	rec.Time = time.Now().Format(time.RFC3339)
	line, _ := json.Marshal(rec)
	f, err := os.OpenFile(gp, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return nil, err
	}
	return &rec, nil
}

func hashFileWith(path, algo string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	var h hash.Hash
	switch strings.ToLower(strings.TrimSpace(algo)) {
	case "", "sha256":
		h = sha256.New()
	case "sha1":
		h = sha1.New()
	case "md5":
		h = md5.New()
	default:
		return "", 0, fmt.Errorf("unsupported hash algo %q (want sha256|sha1|md5)", algo)
	}
	n, err := io.Copy(h, f)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func evalHashEq(rec *GateReceipt, req GateRequest) error {
	algo := strings.ToLower(strings.TrimSpace(req.Algo))
	if algo == "" {
		algo = "sha256"
	}
	gotHash, size, err := hashFileWith(req.Path, algo)
	if err != nil {
		return err
	}
	wantHash := strings.ToLower(strings.TrimSpace(req.Expected))
	sourceLabel := "expected"
	if wantHash == "" && req.OtherPath != "" {
		otherHash, _, err := hashFileWith(req.OtherPath, algo)
		if err != nil {
			return err
		}
		wantHash = otherHash
		sourceLabel = filepath.Base(req.OtherPath)
	}
	if wantHash == "" {
		return fmt.Errorf("hash_eq requires expected (hex) or other_path")
	}
	pass := strings.EqualFold(gotHash, wantHash)
	if pass {
		rec.Status = GatePass
		rec.Summary = fmt.Sprintf("%s(%s) == %s (%d bytes, matched %s)", algo, filepath.Base(req.Path), gotHash, size, sourceLabel)
	} else {
		rec.Status = GateFail
		rec.Summary = fmt.Sprintf("%s(%s) = %s != %s (from %s)", algo, filepath.Base(req.Path), gotHash, wantHash, sourceLabel)
	}
	rec.Metrics = map[string]any{
		"algo":      algo,
		"actual":    gotHash,
		"expected":  wantHash,
		"bytes":     size,
		"reference": sourceLabel,
	}
	return nil
}

func evalFormatValid(rec *GateReceipt, req GateRequest) error {
	want := strings.ToLower(strings.TrimSpace(req.Expected))
	if want == "har" {
		h, err := LoadHAR(req.Path)
		if err != nil || len(h.Entries) == 0 {
			rec.Status = GateFail
			errMsg := "0 entries"
			if err != nil {
				errMsg = err.Error()
			}
			rec.Summary = fmt.Sprintf("HAR parse failed on %s: %s", filepath.Base(req.Path), errMsg)
			rec.Metrics = map[string]any{"kind": "har", "error": errMsg}
			return nil
		}
		rec.Status = GatePass
		rec.Summary = fmt.Sprintf("valid HAR (%d entries) in %s", len(h.Entries), filepath.Base(req.Path))
		rec.Metrics = map[string]any{"kind": "har", "entries": len(h.Entries)}
		return nil
	}

	t, err := TriageFile(req.Path, 4<<20)
	if err != nil {
		return err
	}
	parseErrors := []string{}
	for _, lim := range t.Limits {
		if strings.Contains(lim, "could not parse") {
			parseErrors = append(parseErrors, lim)
		}
	}
	kindMatch := want == "" && t.Kind != "unknown" && !strings.HasPrefix(t.Kind, "xor-masked:")
	if want != "" {
		kindMatch = strings.EqualFold(t.Kind, want) || strings.HasPrefix(strings.ToLower(t.Kind), want)
	}
	if kindMatch && len(parseErrors) == 0 {
		rec.Status = GatePass
		rec.Summary = fmt.Sprintf("valid %s (%d bytes, sha256=%s…)", t.Kind, t.Size, hexPreview([]byte(t.SHA256), 8))
	} else {
		rec.Status = GateFail
		rec.Summary = fmt.Sprintf("format check failed: got kind=%q (want %q), parse_errors=%v", t.Kind, want, parseErrors)
	}
	rec.Metrics = map[string]any{
		"detected_kind": t.Kind,
		"expected_kind": want,
		"size":          t.Size,
		"entropy":       t.Entropy,
		"parse_errors":  parseErrors,
	}
	return nil
}

func evalByteParity(rec *GateReceipt, req GateRequest) error {
	if req.OtherPath == "" {
		return fmt.Errorf("byte_parity requires other_path")
	}
	f1, err := os.Open(req.Path)
	if err != nil {
		return err
	}
	defer f1.Close()
	f2, err := os.Open(req.OtherPath)
	if err != nil {
		return err
	}
	defer f2.Close()

	st1, err := f1.Stat()
	if err != nil {
		return err
	}
	st2, err := f2.Stat()
	if err != nil {
		return err
	}

	var (
		offset    int64
		diffBytes int64
		firstDiff int64 = -1
		buf1            = make([]byte, 64<<10)
		buf2            = make([]byte, 64<<10)
	)
	for {
		n1, e1 := io.ReadFull(f1, buf1)
		n2, e2 := io.ReadFull(f2, buf2)
		m := min(n1, n2)
		for i := 0; i < m; i++ {
			if buf1[i] != buf2[i] {
				diffBytes++
				if firstDiff < 0 {
					firstDiff = offset + int64(i)
				}
			}
		}
		if n1 != n2 {
			extra := int64(max(n1, n2) - m)
			diffBytes += extra
			if firstDiff < 0 {
				firstDiff = offset + int64(m)
			}
		}
		offset += int64(max(n1, n2))
		if (e1 == io.EOF || e1 == io.ErrUnexpectedEOF) && (e2 == io.EOF || e2 == io.ErrUnexpectedEOF) {
			break
		}
		if e1 != nil && e1 != io.EOF && e1 != io.ErrUnexpectedEOF {
			return e1
		}
		if e2 != nil && e2 != io.EOF && e2 != io.ErrUnexpectedEOF {
			return e2
		}
	}

	maxSize := max(st1.Size(), st2.Size())
	if st1.Size() != st2.Size() {
		sizeDelta := st1.Size() - st2.Size()
		if sizeDelta < 0 {
			sizeDelta = -sizeDelta
		}
		if offset < maxSize {
			diffBytes += maxSize - offset
			if firstDiff < 0 {
				firstDiff = min(st1.Size(), st2.Size())
			}
		}
	}

	parity := 1.0
	if maxSize > 0 {
		parity = float64(maxSize-diffBytes) / float64(maxSize)
	}
	pass := diffBytes <= req.MaxDiffBytes && (req.MaxDiffBytes > 0 || st1.Size() == st2.Size())
	if pass {
		rec.Status = GatePass
		rec.Summary = fmt.Sprintf("byte_parity PASS: %d/%d bytes identical (diff=%d <= tol=%d)", maxSize-diffBytes, maxSize, diffBytes, req.MaxDiffBytes)
	} else {
		rec.Status = GateFail
		rec.Summary = fmt.Sprintf("byte_parity FAIL: diff=%d bytes (tol=%d), first_diff_offset=%d, sizes=%d vs %d", diffBytes, req.MaxDiffBytes, firstDiff, st1.Size(), st2.Size())
	}
	rec.Metrics = map[string]any{
		"size_primary":      st1.Size(),
		"size_other":        st2.Size(),
		"diff_bytes":        diffBytes,
		"first_diff_offset": firstDiff,
		"parity_ratio":      math.Round(parity*1e6) / 1e6,
	}
	return nil
}

func evalJSONGolden(rec *GateReceipt, req GateRequest) error {
	if req.OtherPath == "" {
		return fmt.Errorf("json_golden requires other_path")
	}
	tol := req.AbsTol
	if tol <= 0 {
		tol = 1e-6
	}
	b1, err := os.ReadFile(req.Path)
	if err != nil {
		return err
	}
	b2, err := os.ReadFile(req.OtherPath)
	if err != nil {
		return err
	}
	var v1, v2 any
	if err := json.Unmarshal(bytesTrimBOM(b1), &v1); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(req.Path), err)
	}
	if err := json.Unmarshal(bytesTrimBOM(b2), &v2); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(req.OtherPath), err)
	}
	var st jsonDiffState
	st.tol = tol
	diffJSON("$", v1, v2, &st)
	if st.mismatches == 0 {
		rec.Status = GatePass
		rec.Summary = fmt.Sprintf("json_golden PASS: %d/%d leaves matched within abs_tol=%g", st.matched, st.total, tol)
	} else {
		rec.Status = GateFail
		rec.Summary = fmt.Sprintf("json_golden FAIL: %d/%d leaves mismatched (first: %s)", st.mismatches, st.total, st.examples[0])
	}
	rec.Metrics = map[string]any{
		"total_leaves":   st.total,
		"matched_leaves": st.matched,
		"mismatches":     st.mismatches,
		"abs_tol":        tol,
		"examples":       st.examples,
	}
	return nil
}

func bytesTrimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xef && b[1] == 0xbb && b[2] == 0xbf {
		return b[3:]
	}
	return b
}

type jsonDiffState struct {
	tol        float64
	total      int
	matched    int
	mismatches int
	examples   []string
}

func (s *jsonDiffState) fail(path, msg string) {
	s.total++
	s.mismatches++
	if len(s.examples) < 10 {
		s.examples = append(s.examples, path+": "+msg)
	}
}

func (s *jsonDiffState) ok() {
	s.total++
	s.matched++
}

func diffJSON(path string, got, want any, st *jsonDiffState) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			st.fail(path, fmt.Sprintf("got %T, want object", got))
			return
		}
		seen := map[string]bool{}
		for k, wv := range w {
			seen[k] = true
			gv, exists := g[k]
			if !exists {
				st.fail(path+"."+k, "missing key in actual")
				continue
			}
			diffJSON(path+"."+k, gv, wv, st)
		}
		for k := range g {
			if !seen[k] {
				st.fail(path+"."+k, "unexpected key in actual")
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			st.fail(path, fmt.Sprintf("got %T, want array(len=%d)", got, len(w)))
			return
		}
		if len(g) != len(w) {
			st.fail(path, fmt.Sprintf("array length got %d, want %d", len(g), len(w)))
		}
		n := min(len(g), len(w))
		for i := 0; i < n; i++ {
			diffJSON(fmt.Sprintf("%s[%d]", path, i), g[i], w[i], st)
		}
	case float64:
		gf, ok := got.(float64)
		if !ok {
			st.fail(path, fmt.Sprintf("got %v (%T), want number %g", got, got, w))
			return
		}
		diff := math.Abs(gf - w)
		if math.IsNaN(gf) || math.IsNaN(w) || diff > st.tol {
			st.fail(path, fmt.Sprintf("got %g, want %g (|diff|=%g > %g)", gf, w, diff, st.tol))
			return
		}
		st.ok()
	default:
		if got != want {
			st.fail(path, fmt.Sprintf("got %#v, want %#v", got, want))
			return
		}
		st.ok()
	}
}
