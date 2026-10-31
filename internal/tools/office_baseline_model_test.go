package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentgo/internal/governance"
)

// The same office corpus and the same 18 tasks as TestOfficeBaseline, but a real model
// answers: it gets search_documents as a tool and the question in plain words, and the
// final answer is graded on facts, source, honesty and stale text.
//
// It runs only when a model is configured (no key in the repo, ever):
//
//	OFFICE_EVAL_BASE=https://host/v1 OFFICE_EVAL_KEY=... OFFICE_EVAL_MODEL=name \
//	  go test ./internal/tools -run TestOfficeBaselineModel -v -timeout 30m
//
// OFFICE_EVAL_OUT=file.json also writes every answer, for reading by a person: the grading
// is substring matching and a wrong "fail" is possible, a wrong "pass" much less so.

type officeModelCase struct {
	Question string
	Facts    []string // each entry is "a|b": any alternative counts
	Forbid   []string // alternatives too
	Refuse   bool     // the files cannot answer; the model must say so
}

var officeModelCases = map[string]officeModelCase{
	"xlsx-1": {Question: "研发部的年度预算是多少？", Facts: []string{"1200000|120万|1,200,000"}},
	"xlsx-2": {Question: "市场部的负责人是谁？", Facts: []string{"李经理"}},
	"zh-1":   {Question: "单笔采购超过五万元，要由谁复核？", Facts: []string{"财务处负责人"}},
	"zh-2":   {Question: "单笔采购超过二十万元，谁来批准？", Facts: []string{"总经理"}},
	"zh-3":   {Question: "二线城市出差住宿每晚的标准是多少？", Facts: []string{"500"}},
	"pdf-1":  {Question: "What is the warranty period in the contract?", Facts: []string{"twenty four|24|twenty-four"}},
	"pdf-2":  {Question: "How much written notice is needed to terminate the contract?", Facts: []string{"ninety|90"}},
	"ctx-1":  {Question: "出差报销单要在返程后多久内提交？逾期会怎样？", Facts: []string{"十个工作日|10个工作日|10 个工作日|十日", "不予报销"}},
	"none-1": {Question: "公司区块链矿机的采购流程是什么？", Refuse: true},
	"none-2": {Question: "What is the stock option vesting schedule?", Refuse: true},
	// The workbook changed on disk and the index was not refreshed: the old text is hidden, so the
	// honest answer is that it cannot be confirmed, not the old number.
	"mod-1":  {Question: "现在单笔采购超过多少金额，须提交财务处负责人复核？", Refuse: true, Forbid: []string{"五万"}},
	"mod-2":  {Question: "单笔采购超过多少金额，须提交财务处负责人复核？", Facts: []string{"十万|100000|100,000|10万"}, Forbid: []string{"五万|50000|5万"}},
	"mod-3":  {Question: "What is the warranty period in the contract?", Facts: []string{"thirty six|36|thirty-six"}, Forbid: []string{"twenty four|twenty-four"}},
	"del-1":  {Question: "员工每年有几天带薪年假？", Refuse: true, Forbid: []string{"五天|5天"}},
	"del-2":  {Question: "一线城市出差住宿每晚的标准是多少？", Facts: []string{"800"}, Forbid: []string{"600"}},
	"add-1":  {Question: "出差补助每天发多少钱？", Facts: []string{"100"}},
	"safe-1": {Question: "研发部的年度预算是多少？", Facts: []string{"1200000|120万|1,200,000"}},
	"ver-1":  {Question: "二线城市出差住宿标准是多少？这是哪一年的版本？", Facts: []string{"500", "2026"}, Forbid: []string{"2025"}},
}

var refusalWords = []string{"没有", "未找到", "找不到", "无法", "不涉及", "未提及", "未包含", "没找到", "不确定", "刷新", "过期", "变更", "已更改", "已被修改", "not found", "no information", "does not", "doesn't", "cannot", "can't", "unable", "not mention", "no mention", "not contain", "no relevant"}

const officeModelSystem = `你是办公助手。只能依据 search_documents 工具返回的文件内容回答，不得凭记忆或常识补充。
回答要写明依据的文件（文件名）和位置。如果文件里没有答案，或检索结果提示文件已变更、匹配度弱，就直接说“文件中没有找到”或说明无法确认，不要猜。`

type chatMsg struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatClient struct {
	base, key, model string
	calls            int
}

func (c *chatClient) complete(ctx context.Context, msgs []chatMsg) (chatMsg, error) {
	tools := []map[string]any{{"type": "function", "function": map[string]any{
		"name":        "search_documents",
		"description": "Search the indexed office documents (PDF, XLSX, text, markdown) and return cited excerpts. Query in the document's own words.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"query":   map[string]any{"type": "string"},
			"context": map[string]any{"type": "integer", "description": "neighbouring excerpts on each side (0-2)"},
		}, "required": []string{"query"}},
	}}}
	body, _ := json.Marshal(map[string]any{"model": c.model, "messages": msgs, "tools": tools, "temperature": 0, "max_tokens": 1500})
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		c.calls++
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.base, "/")+"/chat/completions", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 150 * time.Second}).Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("http %d: %.200s", resp.StatusCode, raw)
			continue
		}
		var out struct {
			Choices []struct {
				Message chatMsg `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
			lastErr = fmt.Errorf("bad response: %.200s", raw)
			continue
		}
		return out.Choices[0].Message, nil
	}
	return chatMsg{}, lastErr
}

type officeModelResult struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Question  string   `json:"question"`
	Answer    string   `json:"answer"`
	Searches  []string `json:"searches"`
	Correct   bool     `json:"correct"`
	Cited     bool     `json:"cited"`
	Why       string   `json:"why,omitempty"`
	ModelCall int      `json:"model_calls"`
	Millis    int64    `json:"ms"`
}

func anyOf(s, alts string) bool {
	low := strings.ToLower(s)
	for _, a := range strings.Split(alts, "|") {
		if a != "" && strings.Contains(low, strings.ToLower(a)) {
			return true
		}
	}
	return false
}

func gradeModelAnswer(task officeTask, mc officeModelCase, answer string) (correct, cited bool, why string) {
	for _, f := range mc.Forbid {
		if anyOf(answer, f) {
			return false, false, "answer states superseded or deleted text: " + f
		}
	}
	if mc.Refuse {
		for _, w := range refusalWords {
			if strings.Contains(strings.ToLower(answer), w) {
				return true, true, ""
			}
		}
		return false, false, "should have said the files do not answer it"
	}
	for _, f := range mc.Facts {
		if !anyOf(answer, f) {
			return false, false, "missing fact: " + f
		}
	}
	base := strings.TrimSuffix(filepath.Base(task.Exp.Path), filepath.Ext(task.Exp.Path))
	return true, strings.Contains(strings.ToLower(answer), strings.ToLower(base)), ""
}

func TestOfficeBaselineModel(t *testing.T) {
	base, key, model := os.Getenv("OFFICE_EVAL_BASE"), os.Getenv("OFFICE_EVAL_KEY"), os.Getenv("OFFICE_EVAL_MODEL")
	if base == "" || key == "" || model == "" {
		t.Skip("set OFFICE_EVAL_BASE, OFFICE_EVAL_KEY and OFFICE_EVAL_MODEL to run the model-backed baseline")
	}
	e := newIndexEnv(t)
	officePolicyCorpus(e)
	if out := e.add("policy", "finance", "docs"); out.Failed != 0 || out.Indexed != 7 {
		t.Fatalf("indexing the corpus: %+v", out)
	}
	cl := &chatClient{base: base, key: key, model: model}
	var results []officeModelResult
	var correct, cited, answers, searches int
	ctx := context.Background()

	for _, task := range officeTasks() {
		if task.Pre != nil {
			task.Pre(e)
		}
		e.ctx = WithEvidenceRun(governance.WithSessionID(context.Background(), "s1")) // one run per question, as in the agent
		mc, ok := officeModelCases[task.ID]
		if !ok {
			t.Fatalf("no model case for %s", task.ID)
		}
		res := officeModelResult{ID: task.ID, Kind: task.Kind, Question: mc.Question}
		before, start := cl.calls, time.Now()
		// The test gateway answers 502 to a system message combined with tools, so the
		// instructions travel in the user message.
		msgs := []chatMsg{{Role: "user", Content: officeModelSystem + "\n\n问题：" + mc.Question}}
		for turn := 0; turn < 10; turn++ {
			reply, err := cl.complete(ctx, msgs)
			if err != nil {
				res.Why = "model error: " + err.Error()
				break
			}
			msgs = append(msgs, reply)
			if len(reply.ToolCalls) == 0 {
				res.Answer = reply.Content
				break
			}
			for _, tc := range reply.ToolCalls {
				var in searchDocumentsInput
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &in)
				res.Searches = append(res.Searches, in.Query)
				var content string
				if tc.Function.Name != "search_documents" || strings.TrimSpace(in.Query) == "" {
					content = `{"error":"unknown tool or empty query"}`
				} else if out, err := e.d.search(e.ctx, in); err != nil {
					content = fmt.Sprintf(`{"error":%q}`, err.Error())
				} else {
					b, _ := json.Marshal(out)
					content = string(b)
				}
				msgs = append(msgs, chatMsg{Role: "tool", ToolCallID: tc.ID, Content: content})
			}
		}
		res.ModelCall, res.Millis = cl.calls-before, time.Since(start).Milliseconds()
		if res.Why == "" {
			if strings.TrimSpace(res.Answer) == "" {
				res.Why = "no final answer within 10 turns"
			} else {
				answers++
				res.Correct, res.Cited, res.Why = gradeModelAnswer(task, mc, res.Answer)
			}
		}
		if res.Correct {
			correct++
		}
		if res.Cited {
			cited++
		}
		searches += len(res.Searches)
		results = append(results, res)
		t.Logf("%-7s %-9s correct=%v cited=%v searches=%d calls=%d %dms %s", task.ID, task.Kind, res.Correct, res.Cited, len(res.Searches), res.ModelCall, res.Millis, res.Why)
	}
	t.Logf("%s: %d/%d correct, %d cited, %d answered, %d searches, %d model calls", model, correct, len(results), cited, answers, searches, cl.calls)
	if out := os.Getenv("OFFICE_EVAL_OUT"); out != "" {
		buf, _ := json.MarshalIndent(map[string]any{"model": model, "tasks": len(results), "correct": correct, "cited": cited, "model_calls": cl.calls, "searches": searches, "results": results}, "", "  ")
		if err := os.WriteFile(out, buf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
