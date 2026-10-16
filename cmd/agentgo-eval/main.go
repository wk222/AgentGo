// agentgo-eval runs a fixed task suite against a running `agentgo --serve` and
// compares the outcome with a saved baseline (pass rate, tokens, cache hits).
//
//	agentgo --serve                                   # terminal 1
//	agentgo-eval --out docs/eval/current.json         # terminal 2
//	agentgo-eval --out cur.json --baseline docs/eval/baseline.json
//
// Exit code: 0 ok, 1 regression against the baseline, 2 usage or runtime error.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"

	"agentgo/internal/evaluation"
)

type serveInfo struct {
	Addr  string `json:"addr"`
	Token string `json:"token"`
}

func main() { os.Exit(run()) }

func run() int {
	var (
		suitePath  = flag.String("suite", "", "suite JSON file (default: built-in suite)")
		runs       = flag.Int("runs", 1, "runs per case; a case passes only if every run passes")
		out        = flag.String("out", "", "write the result JSON here")
		baseline   = flag.String("baseline", "", "compare with this result JSON; regression => exit 1")
		addr       = flag.String("addr", "", "gateway address host:port (default: from agentgo-serve.json)")
		token      = flag.String("token", "", "gateway token (default: from agentgo-serve.json)")
		infoPath   = flag.String("serve-info", "", "path of agentgo-serve.json (default: <data dir>/agentgo-serve.json)")
		harness    = flag.String("harness", "", "run every session as this harness preset (minimal|standard|ultimate|creative); default: server default")
		timeout    = flag.Duration("case-timeout", 5*time.Minute, "timeout of one run")
		maxPass    = flag.Float64("max-pass-drop", evaluation.DefaultThresholds().MaxPassDrop, "allowed pass-rate drop vs baseline (0.10 = 10 points)")
		maxTokens  = flag.Float64("max-token-growth", evaluation.DefaultThresholds().MaxTokenGrowth, "allowed growth of prompt tokens per run vs baseline")
		maxCacheDr = flag.Float64("max-cache-drop", evaluation.DefaultThresholds().MaxCacheHitDrop, "cache-hit drop that triggers a warning")
	)
	flag.Parse()

	set, err := loadSuite(*suitePath)
	if err != nil {
		return fail("suite: %v", err)
	}
	base, tok, err := resolveGateway(*addr, *token, *infoPath)
	if err != nil {
		return fail("%v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	httpAgent := &evaluation.HTTPAgent{BaseURL: base, Token: tok, Harness: *harness}
	// Cases that do not apply to a preset (e.g. write/approval cases under read-only
	// "minimal") are dropped; without --harness the server's default preset decides.
	effective := *harness
	if effective == "" {
		effective = httpAgent.DefaultHarness(ctx)
	}
	set = set.ForHarness(effective)
	agent := &timeoutAgent{inner: httpAgent, d: *timeout}
	fmt.Printf("suite %s: %d cases x %d runs against %s (harness: %s)\n", set.ID, len(set.Cases), *runs, base, orDefault(*harness))
	// Serial on purpose: stable timings and one backend session at a time.
	res, err := evaluation.EvaluateSetTagged(ctx, agent, set, *runs, false, evaluation.DefaultRunTag())
	if err != nil {
		return fail("evaluate: %v", err)
	}
	res.Harness = *harness
	printReport(res)

	if *out != "" {
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return fail("%v", err)
		}
		if err := evaluation.SaveResult(*out, res); err != nil {
			return fail("write result: %v", err)
		}
		fmt.Println("result written to", *out)
	}

	if *baseline == "" {
		return 0
	}
	prev, err := evaluation.LoadResult(*baseline)
	if err != nil {
		return fail("baseline: %v", err)
	}
	findings := evaluation.Compare(prev, res, evaluation.Thresholds{
		MaxPassDrop: *maxPass, MaxTokenGrowth: *maxTokens, MaxCacheHitDrop: *maxCacheDr,
	})
	if len(findings) == 0 {
		fmt.Println("vs baseline: no regression")
		return 0
	}
	for _, f := range findings {
		level := "WARN"
		if f.Fail {
			level = "FAIL"
		}
		fmt.Printf("vs baseline: %s %s\n", level, f.Msg)
	}
	if evaluation.HasFailure(findings) {
		return 1
	}
	return 0
}

func orDefault(s string) string {
	if s == "" {
		return "server default"
	}
	return s
}

func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "agentgo-eval: "+format+"\n", a...)
	return 2
}

func loadSuite(path string) (evaluation.EvalSet, error) {
	if path == "" {
		return evaluation.DefaultSuite()
	}
	return evaluation.LoadSuite(path)
}

// resolveGateway returns the base URL and token. Flags win over agentgo-serve.json.
// The data dir rule mirrors bridge.DefaultDataDir (kept local so this tool does not
// link the whole desktop runtime).
func resolveGateway(addr, token, infoPath string) (string, string, error) {
	if addr == "" {
		if infoPath == "" {
			p, err := xdg.DataFile("agentgo")
			if err != nil {
				return "", "", fmt.Errorf("cannot locate data dir: %w", err)
			}
			infoPath = filepath.Join(filepath.Dir(p), "agentgo-serve.json")
		}
		raw, err := os.ReadFile(infoPath)
		if err != nil {
			return "", "", fmt.Errorf("no running gateway found (%v); start `agentgo --serve` or pass --addr/--token", err)
		}
		var info serveInfo
		if err := json.Unmarshal(raw, &info); err != nil || info.Addr == "" {
			return "", "", fmt.Errorf("bad %s", infoPath)
		}
		addr = info.Addr
		if token == "" {
			token = info.Token
		}
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	return addr, token, nil
}

// timeoutAgent bounds every single run so a stuck model call cannot hang the suite.
type timeoutAgent struct {
	inner *evaluation.HTTPAgent
	d     time.Duration
}

func (t *timeoutAgent) Generate(ctx context.Context, sid, in string) (string, error) {
	out, _, err := t.GenerateMeasured(ctx, sid, in)
	return out, err
}

func (t *timeoutAgent) GenerateMeasured(ctx context.Context, sid, in string) (string, evaluation.Metrics, error) {
	ctx, cancel := context.WithTimeout(ctx, t.d)
	defer cancel()
	return t.inner.GenerateMeasured(ctx, sid, in)
}

func printReport(r *evaluation.EvalResult) {
	fmt.Println()
	for _, c := range r.Cases {
		mark := "PASS"
		if !c.Passed {
			mark = "FAIL"
		}
		var m evaluation.Metrics
		var dur time.Duration
		note := ""
		for _, d := range c.Details {
			m.Add(d.Metrics)
			dur += d.Duration
			if d.ErrorMsg != "" && note == "" {
				note = "  <- " + d.ErrorMsg
			}
		}
		fmt.Printf("  %s  %-24s %5.1fs  prompt=%-6d cached=%-6d out=%-5d tools=%d%s\n",
			mark, c.CaseID, dur.Seconds(), m.PromptTokens, m.CachedTokens, m.CompletionTokens, m.ToolCalls, note)
	}
	fmt.Printf("\npassed %d/%d cases (%.0f%%), %d runs, %.1fs\n",
		countPassed(r), len(r.Cases), r.PassingRate*100, r.TotalRuns, r.Duration.Seconds())
	fmt.Printf("tokens: prompt=%d cached=%d (hit %.0f%%) completion=%d reasoning=%d, tool calls=%d, model calls=%d\n",
		r.Totals.PromptTokens, r.Totals.CachedTokens, r.Totals.CacheHitRate()*100,
		r.Totals.CompletionTokens, r.Totals.ReasoningTokens, r.Totals.ToolCalls, r.Totals.ModelCalls)
}

func countPassed(r *evaluation.EvalResult) int {
	n := 0
	for _, c := range r.Cases {
		if c.Passed {
			n++
		}
	}
	return n
}
