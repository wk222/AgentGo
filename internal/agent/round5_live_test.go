//go:build round4live

package agent

// Round 5 live: the nonexistent-software control. See round5_ghost_test.go
// and docs/harness-lab-round5.md (pre-registration) for the design.
//
//	$env:ROUND5_MODELS   default "deepseek-v4-flash,qwen/qwen3.7-max"
//	$env:ROUND5_REPEATS  default 5
//	$env:ROUND5_ARMS     optional comma-separated arm filter
//	$env:ROUND4_OUT      JSONL appended one record per run
//
//	go test ./internal/agent/ -tags round4live -run TestRound5Ghost -count=1 -timeout=2h -v

import (
	"math/rand"
	"os"
	"sort"
	"testing"
	"time"
)

func TestRound5Ghost(t *testing.T) {
	key, base := round4LiveEnv(t)
	models := round4EnvList("ROUND5_MODELS", "deepseek-v4-flash,qwen/qwen3.7-max")
	reps := round4EnvInt("ROUND5_REPEATS", 5)
	parallel := round4EnvInt("ROUND4_PARALLEL", 4)
	timeout := time.Duration(round4EnvInt("ROUND4_TIMEOUT_S", 180)) * time.Second
	canvas := round4Canvas()

	// ROUND5_VARIANT=decoy: fictional decoy files in the sandbox, the
	// amplified 4-arms x memory-on/off set, engagement (L1/L2/L3) primary.
	decoy := os.Getenv("ROUND5_VARIANT") == "decoy"
	task := round5GhostTask()
	armPool := round5GhostArms()
	anchorSuffix := "v1"
	if decoy {
		task = round5GhostDecoyTask()
		armPool = round5GhostArmsDecoy()
		anchorSuffix = "v2-decoy"
	}

	armFilter := map[string]bool{}
	for _, n := range round4EnvList("ROUND5_ARMS", "") {
		armFilter[n] = true
	}
	var arms []round5GhostArm
	for _, a := range armPool {
		if len(armFilter) == 0 || armFilter[a.Name] {
			arms = append(arms, a)
		}
	}
	sink := newRound4Sink(t)
	rng := rand.New(rand.NewSource(int64(round4EnvInt("ROUND4_SEED", 20261031))))

	type job struct {
		model string
		arm   round5GhostArm
		rep   int
		order int
	}
	var all []job
	for _, m := range models {
		for r := 0; r < reps; r++ {
			perm := rng.Perm(len(arms))
			for order, idx := range perm {
				all = append(all, job{m, arms[idx], r, order})
			}
		}
	}
	rng.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })
	t.Logf("round5 ghost: %d models x %d arms x %d repeats = %d runs, parallel=%d", len(models), len(arms), reps, len(all), parallel)

	var jobs []func()
	for _, j := range all {
		j := j
		jobs = append(jobs, func() {
			pin := ""
			anchor := ""
			if j.arm.Pin {
				pin = round5GhostPin
				anchor = "ghost|pinned_refusal|" + anchorSuffix
			}
			cfg := LLMSettings{APIBase: base, APIKey: key, Model: j.model}
			rec := runRound4(t, round4RunSpec{Cfg: cfg, Task: task, Arm: j.arm.Name, Strategy: j.arm.Strategy,
				PinText: pin, PostNudge: j.arm.PostNudge, View: j.arm.View, MemoryOff: j.arm.MemoryOff,
				AnchorID: anchor, Order: j.order, Repeat: j.rep, Canvas: canvas, Timeout: timeout})
			sink.add(rec)
			class := round5ClassifyGhost(rec)
			eng := round5Engage(rec)
			valid := "valid"
			if j.arm.View != "" && !rec.ViewRewritten {
				valid = "INVALID: view not rewritten"
			}
			if j.arm.Pin && rec.LLMCalls > 0 && !rec.TaskInContext {
				valid = "INVALID: task lost from context"
			}
			t.Logf("[%s | %s r%d] outcome=%s engage_L1=%v engage_L2=%v engage_L3=%v mutating=%d armor=%v view_rewritten=%v memory_off=%v tools=%d llm=%d %dms %s err=%q harness=%q",
				rec.Model, rec.Arm, rec.Repeat, class.Outcome, eng.AnyTool, eng.Search, eng.Targeted, class.MutatingAttempts,
				rec.ArmorTriggered, rec.ViewRewritten, rec.MemoryOff, rec.ToolCalls, rec.LLMCalls,
				rec.DurationMS, valid, clip(rec.Error, 80), clip(rec.HarnessError, 80))
			if os.Getenv("ROUND5_VERBOSE") == "1" {
				t.Logf("  final: %s", clip(round5Answer(rec), 400))
			}
		})
	}
	round4Pool(parallel, jobs)

	// Compact outcome + engagement table for the log. Engagement (real tool
	// calls only) is the primary behavioural metric; outcomes stay separate
	// and are never merged with it.
	counts := map[string]map[string]int{}
	for _, rec := range sink.recs {
		k := rec.Model + " | " + rec.Arm
		if counts[k] == nil {
			counts[k] = map[string]int{}
		}
		counts[k][round5ClassifyGhost(rec).Outcome]++
		counts[k]["_runs"]++
		e := round5Engage(rec)
		if e.AnyTool {
			counts[k]["engage_L1"]++
		}
		if e.Search {
			counts[k]["engage_L2"]++
		}
		if e.Targeted {
			counts[k]["engage_L3"]++
		}
		if e.Mutating {
			counts[k]["engage_mutating"]++
		}
	}
	var keys []string
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("%-52s %v", k, counts[k])
	}
	round4WriteSummary(t, sink.recs)
}
