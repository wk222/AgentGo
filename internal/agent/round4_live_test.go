//go:build round4live

package agent

// Round 4 live experiments against a real model through AgentGo's production
// Generate path. The key is read from the environment only.
//
//	$env:LAB_API_KEY  = "..."                            (required)
//	$env:LAB_BASE_URL = "https://gateway.example.invalid/v1" (required; no default is baked in)
//	$env:ROUND4_MODELS, ROUND4_TASKS, ROUND4_CATEGORIES, ROUND4_REPEATS,
//	$env:ROUND4_PARALLEL, ROUND4_CANVAS, ROUND4_TIMEOUT_S, ROUND4_SEED
//	$env:ROUND4_OUT      JSONL appended one record per run
//
//	go test ./internal/agent/ -tags round4live -run TestRound4Harvest -count=1 -timeout=6h -v
//	go test ./internal/agent/ -tags round4live -run TestRound4Replay  -count=1 -timeout=6h -v   (needs ROUND4_ANCHORS)
//	go test ./internal/agent/ -tags round4live -run TestRound4Probe   -count=1 -timeout=1h -v
//
// Harvest runs the baseline arm (no armor) and records which runs ended in a
// zero-tool stall. Replay takes those stalls as native anchors and continues
// each from the same pinned first completion under every arm. Grading is by
// disk state only; model claims are recorded but never used as evidence.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func round4EnvList(name, def string) []string {
	v := os.Getenv(name)
	if strings.TrimSpace(v) == "" {
		v = def
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func round4EnvInt(name string, def int) int {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func round4LiveEnv(t *testing.T) (key, base string) {
	t.Helper()
	key = os.Getenv("LAB_API_KEY")
	if key == "" {
		t.Skip("LAB_API_KEY not set")
	}
	base = os.Getenv("LAB_BASE_URL")
	if base == "" {
		t.Skip("LAB_BASE_URL not set")
	}
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	return key, base
}

type round4Sink struct {
	mu   sync.Mutex
	f    *os.File
	enc  *json.Encoder
	recs []round4RunRecord
}

func newRound4Sink(t *testing.T) *round4Sink {
	t.Helper()
	out := os.Getenv("ROUND4_OUT")
	if out == "" {
		out = filepath.Join(os.TempDir(), "round4-results.jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("writing records to %s", out)
	t.Cleanup(func() { _ = f.Close() })
	return &round4Sink{f: f, enc: json.NewEncoder(f)}
}

func (s *round4Sink) add(rec round4RunRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.enc.Encode(rec)
	_ = s.f.Sync()
	s.recs = append(s.recs, rec)
}

// round4Pool runs jobs on n workers and returns when all are done.
func round4Pool(n int, jobs []func()) {
	if n < 1 {
		n = 1
	}
	ch := make(chan func())
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				j()
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()
}

func round4Canvas() ExecutionCanvas {
	switch strings.ToLower(os.Getenv("ROUND4_CANVAS")) {
	case "deep":
		return CanvasDeep
	case "focused":
		return CanvasFocused
	}
	return CanvasBalanced
}

func round4SelectTasks() []round4Task {
	want := map[string]bool{}
	for _, id := range round4EnvList("ROUND4_TASKS", "") {
		want[id] = true
	}
	cats := map[string]bool{}
	for _, c := range round4EnvList("ROUND4_CATEGORIES", "") {
		cats[c] = true
	}
	var out []round4Task
	for _, task := range round4Tasks() {
		if len(want) > 0 && !want[task.ID] {
			continue
		}
		if len(cats) > 0 && !cats[task.Category] {
			continue
		}
		out = append(out, task)
	}
	return out
}

func round4LogLine(t *testing.T, rec round4RunRecord) {
	t.Helper()
	t.Logf("[%s | %s | %s r%d] verified=%v tools=%d stall=%v gate=%v armor=%v unauth(att/eff)=%v/%v deny=%d llm=%d %dms err=%q harness=%q",
		rec.Model, rec.TaskID, rec.Arm, rec.Repeat, rec.Verdict.VerifiedCompletion, rec.ToolCalls,
		round4ZeroToolStall(rec), rec.GateHit, rec.ArmorTriggered,
		rec.Verdict.UnauthorizedAttempt, rec.Verdict.UnauthorizedEffect, rec.GovernanceDenials,
		rec.LLMCalls, rec.DurationMS, clip(rec.Error, 80), clip(rec.HarnessError, 80))
}

func round4Summary(recs []round4RunRecord) string {
	type agg struct {
		runs, harness, errs, stalls, gate, verified, authorized, att, eff, falseC, toolRuns, iter, deny int
	}
	by := map[string]*agg{}
	key := func(r round4RunRecord) string { return r.Model + " | " + r.Arm }
	for _, r := range recs {
		a := by[key(r)]
		if a == nil {
			a = &agg{}
			by[key(r)] = a
		}
		a.runs++
		if r.HarnessError != "" {
			a.harness++
			continue
		}
		if r.Error != "" {
			a.errs++
		}
		if round4ZeroToolStall(r) {
			a.stalls++
			if r.GateHit {
				a.gate++
			}
		}
		if r.ToolCalls > 0 {
			a.toolRuns++
		}
		if r.Verdict.VerifiedCompletion {
			a.verified++
		}
		if r.Verdict.AuthorizedCompletion {
			a.authorized++
		}
		if r.Verdict.UnauthorizedAttempt {
			a.att++
		}
		if r.Verdict.UnauthorizedEffect {
			a.eff++
		}
		if r.Verdict.FalseCompletion {
			a.falseC++
		}
		if r.HitIterLimit {
			a.iter++
		}
		a.deny += r.GovernanceDenials
	}
	var keys []string
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		a := by[k]
		b.WriteString(fmt.Sprintf("%-44s runs=%d harness_err=%d err=%d tool_runs=%d zero_tool_stalls=%d(gate=%d) verified=%d authorized=%d unauth_attempt=%d unauth_effect=%d false_completion=%d iter_limit=%d denials=%d\n",
			k, a.runs, a.harness, a.errs, a.toolRuns, a.stalls, a.gate, a.verified, a.authorized, a.att, a.eff, a.falseC, a.iter, a.deny))
	}
	return b.String()
}

func round4WriteSummary(t *testing.T, recs []round4RunRecord) {
	s := round4Summary(recs)
	t.Log("\n" + s)
	if p := os.Getenv("ROUND4_SUMMARY"); p != "" {
		_ = os.WriteFile(p, []byte(s), 0o644)
	}
}

// TestRound4Harvest: baseline (no armor) over models x tasks x repeats.
func TestRound4Harvest(t *testing.T) {
	key, base := round4LiveEnv(t)
	models := round4EnvList("ROUND4_MODELS", "qwen/qwen3.7-max,deepseek-v3.1")
	tasks := round4SelectTasks()
	repeats := round4EnvInt("ROUND4_REPEATS", 1)
	// ROUND4_REPEAT_BASE shifts repeat numbering so anchors (model|task|repeat)
	// from separate harvest files never collide when the files are merged.
	repeatBase := round4EnvInt("ROUND4_REPEAT_BASE", 0)
	parallel := round4EnvInt("ROUND4_PARALLEL", 4)
	timeout := time.Duration(round4EnvInt("ROUND4_TIMEOUT_S", 180)) * time.Second
	canvas := round4Canvas()
	sink := newRound4Sink(t)

	type job struct {
		model string
		task  round4Task
		rep   int
	}
	var all []job
	for _, m := range models {
		for _, task := range tasks {
			for r := 0; r < repeats; r++ {
				all = append(all, job{m, task, repeatBase + r})
			}
		}
	}
	rng := rand.New(rand.NewSource(int64(round4EnvInt("ROUND4_SEED", 20261031))))
	rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	t.Logf("harvest: %d runs = %d models x %d tasks x %d repeats, parallel=%d canvas=%s", len(all), len(models), len(tasks), repeats, parallel, canvas)

	// ROUND4_NO_BANNER=1 is the banner-ablation diagnostic: same governance and
	// session mode, but the model is shown the plain assistant system prompt.
	noBanner := os.Getenv("ROUND4_NO_BANNER") == "1"
	arm := "baseline"
	if noBanner {
		arm = "baseline_nobanner"
	}
	var jobs []func()
	for _, j := range all {
		j := j
		jobs = append(jobs, func() {
			cfg := LLMSettings{APIBase: base, APIKey: key, Model: j.model}
			rec := runRound4(t, round4RunSpec{Cfg: cfg, Task: j.task, Arm: arm, Strategy: ArmorStrategyNone,
				StripBanner: noBanner, Repeat: j.rep, Canvas: canvas, Timeout: timeout})
			sink.add(rec)
			round4LogLine(t, rec)
		})
	}
	round4Pool(parallel, jobs)
	round4WriteSummary(t, sink.recs)
}

func round4ReadRecords(path string) ([]round4RunRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []round4RunRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r round4RunRecord
		if json.Unmarshal([]byte(line), &r) == nil {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

// TestRound4Replay: every native anchor x every arm x repeats, all starting
// from the same pinned first completion and a fresh identical sandbox.
func TestRound4Replay(t *testing.T) {
	key, base := round4LiveEnv(t)
	src := os.Getenv("ROUND4_ANCHORS")
	if src == "" {
		t.Skip("ROUND4_ANCHORS not set (JSONL of harvest records)")
	}
	recs, err := round4ReadRecords(src)
	if err != nil {
		t.Fatal(err)
	}
	anchors := round4AnchorsFromRecords(recs)
	if models := round4EnvList("ROUND4_MODELS", ""); len(models) > 0 {
		keep := map[string]bool{}
		for _, m := range models {
			keep[m] = true
		}
		var f []round4Anchor
		for _, a := range anchors {
			if keep[a.Model] {
				f = append(f, a)
			}
		}
		anchors = f
	}
	armNames := map[string]bool{}
	for _, n := range round4EnvList("ROUND4_ARMS", "") {
		armNames[n] = true
	}
	var arms []round4ReplayArm
	for _, a := range round4ReplayArms {
		if len(armNames) == 0 || armNames[a.Name] {
			arms = append(arms, a)
		}
	}
	reps := round4EnvInt("ROUND4_REPLAY_REPEATS", 2)
	parallel := round4EnvInt("ROUND4_PARALLEL", 4)
	timeout := time.Duration(round4EnvInt("ROUND4_TIMEOUT_S", 180)) * time.Second
	canvas := round4Canvas()
	sink := newRound4Sink(t)
	rng := rand.New(rand.NewSource(int64(round4EnvInt("ROUND4_SEED", 20261031))))

	type job struct {
		anchor round4Anchor
		arm    round4ReplayArm
		rep    int
		order  int
	}
	var all []job
	for _, a := range anchors {
		for r := 0; r < reps; r++ {
			perm := rng.Perm(len(arms))
			for order, idx := range perm {
				all = append(all, job{a, arms[idx], r, order})
			}
		}
	}
	rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	t.Logf("replay: %d anchors x %d arms x %d repeats = %d runs, parallel=%d", len(anchors), len(arms), reps, len(all), parallel)

	var jobs []func()
	for _, j := range all {
		j := j
		jobs = append(jobs, func() {
			task, ok := round4TaskByID(j.anchor.TaskID)
			if !ok {
				t.Errorf("anchor %s references unknown task", j.anchor.ID)
				return
			}
			pin := ""
			if j.arm.Pin {
				pin = j.anchor.StallText
			}
			cfg := LLMSettings{APIBase: base, APIKey: key, Model: j.anchor.Model}
			rec := runRound4(t, round4RunSpec{Cfg: cfg, Task: task, Arm: j.arm.Name, Strategy: j.arm.Strategy,
				PinText: pin, PostNudge: j.arm.PostNudge, View: j.arm.View, AnchorID: j.anchor.ID,
				Order: j.order, Repeat: j.rep, Canvas: canvas, Timeout: timeout})
			sink.add(rec)
			round4LogLine(t, rec)
		})
	}
	round4Pool(parallel, jobs)
	round4WriteSummary(t, sink.recs)
}

// TestRound4Probe: does a model use native tool calling at all through this
// gateway and AgentGo? Four representative tasks, one repeat each.
func TestRound4Probe(t *testing.T) {
	key, base := round4LiveEnv(t)
	models := round4EnvList("ROUND4_MODELS", "")
	if len(models) == 0 {
		t.Skip("ROUND4_MODELS not set")
	}
	ids := []string{"csv_finance", "kill_port_8080", "t_tmp_files", "f_delete_silent"}
	parallel := round4EnvInt("ROUND4_PARALLEL", 4)
	timeout := time.Duration(round4EnvInt("ROUND4_TIMEOUT_S", 180)) * time.Second
	sink := newRound4Sink(t)
	var jobs []func()
	for _, m := range models {
		for _, id := range ids {
			m, id := m, id
			jobs = append(jobs, func() {
				task, _ := round4TaskByID(id)
				cfg := LLMSettings{APIBase: base, APIKey: key, Model: m}
				rec := runRound4(t, round4RunSpec{Cfg: cfg, Task: task, Arm: "probe", Strategy: ArmorStrategyNone,
					Canvas: round4Canvas(), Timeout: timeout})
				sink.add(rec)
				round4LogLine(t, rec)
			})
		}
	}
	round4Pool(parallel, jobs)
	round4WriteSummary(t, sink.recs)
}
