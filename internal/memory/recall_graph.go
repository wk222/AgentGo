package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/compose"
)

type RecallInput struct {
	Query  string
	Opts   RecallOptions
	Config RecallRuntimeConfig
	Engine *HybridEngine
}

type RecallResult struct {
	Input      RecallInput
	FTSRows    []Record
	FTSErr     error
	MilvusRows []Record
	TimedOut   bool
	Degraded   bool // true when results came from the SQLite LIKE fallback scan rather than FTS/vector
	RctxErr    error
}

type parsedQueryRewrite struct {
	Query             string  `json:"query"`
	Modality          string  `json:"modality"`
	TimeOffsetSeconds int64   `json:"time_offset_seconds"`
	MinImportance     float64 `json:"min_importance"`
}

var systemPrompt = `你是一个检索查询重写与条件提取助手。
根据最近的对话历史，把用户当前的对话输入重写为一个适合用于向量 and 全文检索的独立、明确的语义检索词（Query），并提取任何隐藏的元数据过滤条件。
你必须只返回一个 JSON 对象，格式如下：
{
  "query": "重写后的纯语义检索词",
  "modality": "episode（如果用户提到'对话'/'聊过'），journal（如果用户提到'日记'/'日志'），fact（如果用户提到'事实'/'代码'/'知识'），如果未提及则为空字符串",
  "time_offset_seconds": 相对当前时间的秒数过滤，例如用户提到“刚才”/“刚才聊的”提取为 300，“今天”/“今天早些时候”提取为 86400，“昨天”提取为 172800，“上周”/“上个礼拜”提取为 604800，如果未提及则为 0,
  "min_importance": 0.0 到 2.0 之间的重要度阈值，默认为 0.0
}

注意：如果不需要重写，且无任何时间/模态过滤，直接在 query 中放入原查询，其他字段设为空或0。只输出合法 JSON 文本，不要使用三反引号包含的 markdown 标记。`

func rewriteQueryLLM(ctx context.Context, apiBase, apiKey, query, historyStr string) parsedQueryRewrite {
	defaultRes := parsedQueryRewrite{Query: query}
	if apiKey == "" {
		return defaultRes
	}
	base := strings.TrimRight(apiBase, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	model := os.Getenv("AGENTGO_REWRITE_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}

	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": fmt.Sprintf("对话历史:\n%s\n\n当前输入: %s\n输出:", historyStr, query)},
		},
		"temperature": 0.0,
		"max_tokens":  200,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return defaultRes
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return defaultRes
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return defaultRes
	}

	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil || len(res.Choices) == 0 {
		return defaultRes
	}
	content := strings.TrimSpace(res.Choices[0].Message.Content)
	if content == "" {
		return defaultRes
	}
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var parsed parsedQueryRewrite
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return defaultRes
	}
	if parsed.Query == "" {
		parsed.Query = query
	}
	return parsed
}

// BuildRecallGraph builds the Eino compose.Graph for parallel retrieval and reciprocal rank fusion (RRF) merging.
func BuildRecallGraph(ctx context.Context) (compose.Runnable[RecallInput, []Record], error) {
	g := compose.NewGraph[RecallInput, []Record]()

	// Node 0: query rewrite & self-query metadata parsing
	rewriteLambda := compose.InvokableLambda(func(ctx context.Context, in RecallInput) (RecallInput, error) {
		if in.Engine == nil || in.Engine.apiKey == "" || in.Opts.Scope == "" {
			return in, nil
		}

		// Fetch recent 6 episode records (which are user/assistant turns)
		history, err := in.Engine.sqlite.ListByModality(ctx, "episode", in.Opts.Scope, 6)
		if err != nil || len(history) == 0 {
			return in, nil
		}

		// Reverse to chronological order (oldest first)
		for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
			history[i], history[j] = history[j], history[i]
		}

		var lines []string
		for _, h := range history {
			content := h.Content
			if len(content) > 300 {
				content = content[:300] + "..."
			}
			lines = append(lines, content)
		}
		historyStr := strings.Join(lines, "\n---\n")

		parsed := rewriteQueryLLM(ctx, in.Engine.apiBase, in.Engine.apiKey, in.Query, historyStr)
		if parsed.Query != in.Query {
			log.Printf("[memory] Rewrite query: %q -> %q", in.Query, parsed.Query)
			in.Query = parsed.Query
		}
		if parsed.Modality != "" {
			in.Opts.Modality = parsed.Modality
			log.Printf("[memory] Filter modality: %q", parsed.Modality)
		}
		if parsed.MinImportance > 0 {
			in.Opts.MinImportance = parsed.MinImportance
			log.Printf("[memory] Filter min_importance: %f", parsed.MinImportance)
		}
		if parsed.TimeOffsetSeconds > 0 {
			now := time.Now().Unix()
			in.Opts.StartTime = now - parsed.TimeOffsetSeconds
			log.Printf("[memory] Filter start_time: %d (%s ago)", in.Opts.StartTime, time.Duration(parsed.TimeOffsetSeconds)*time.Second)
		}

		return in, nil
	})

	// Node 1: retrieve
	retrieveLambda := compose.InvokableLambda(func(ctx context.Context, in RecallInput) (RecallResult, error) {
		if in.Engine == nil {
			return RecallResult{Input: in}, nil
		}

		if in.Opts.Limit <= 0 {
			in.Opts.Limit = 10
		}
		fetchOpts := in.Opts
		fetchLimit := in.Opts.Limit * 2
		if fetchLimit < 12 {
			fetchLimit = 12
		}
		fetchOpts.Limit = fetchLimit

		rctx, cancel := context.WithTimeout(ctx, in.Config.Timeout)
		defer cancel()

		var (
			ftsRows    []Record
			milvusRows []Record
			ftsErr     error
			wg         sync.WaitGroup
		)

		wg.Add(1)
		go func() {
			defer wg.Done()
			ftsRows, ftsErr = in.Engine.recallFTS(rctx, in.Query, fetchOpts)
		}()

		if in.Engine.milvus != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				milvusRows, _ = in.Engine.milvus.Recall(rctx, in.Query, fetchOpts)
			}()
		}

		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		timedOut := false
		select {
		case <-done:
		case <-rctx.Done():
			timedOut = true
		}

		degraded := false
		if len(ftsRows) == 0 {
			reason := "FTS returned no rows"
			if ftsErr != nil {
				reason = "FTS error: " + ftsErr.Error()
			} else if timedOut {
				reason = fmt.Sprintf("FTS timed out after %s", in.Config.Timeout)
			}
			log.Printf("[memory] DEGRADE: %s — falling back to SQLite LIKE scan (no embedding/vector, weaker relevance) query=%q scope=%q", reason, in.Query, in.Opts.Scope)
			degraded = true
			fbCtx := context.WithoutCancel(ctx)
			if rows, err := in.Engine.sqlite.RecallLikeFallback(fbCtx, in.Query, in.Opts); err != nil {
				log.Printf("[memory] DEGRADE: LIKE fallback also failed: %v", err)
			} else if len(rows) > 0 {
				if in.Engine.pipeline != nil {
					ftsRows = in.Engine.pipeline.RankRecords(in.Query, rows)
				} else {
					ftsRows = rows
				}
				log.Printf("[memory] DEGRADE: LIKE fallback recovered %d row(s)", len(ftsRows))
			} else {
				log.Printf("[memory] DEGRADE: LIKE fallback returned 0 rows")
			}
		} else if timedOut {
			log.Printf("[memory] recall timeout (%s): FTS-only degrade (Milvus skipped)", in.Config.Timeout)
		}

		milvusRows = filterRecordsByScope(milvusRows, in.Opts.Scope)

		return RecallResult{
			Input:      in,
			FTSRows:    ftsRows,
			FTSErr:     ftsErr,
			MilvusRows: milvusRows,
			TimedOut:   timedOut,
			Degraded:   degraded,
			RctxErr:    rctx.Err(),
		}, nil
	})

	// Node 2: merge
	mergeLambda := compose.InvokableLambda(func(ctx context.Context, res RecallResult) ([]Record, error) {
		lists := [][]Record{res.FTSRows, res.MilvusRows}
		weights := []float64{res.Input.Config.FTSWeight, res.Input.Config.MilvusWeight}
		merged := mergeHybridRRF(lists, weights, res.Input.Opts.Limit)

		if res.Input.Config.EmbedRerank && res.RctxErr == nil && res.Input.Engine != nil && res.Input.Engine.embedder != nil && res.Input.Engine.embedder.cfg.APIKey != "" {
			fetchOpts := res.Input.Opts
			fetchLimit := res.Input.Opts.Limit * 2
			if fetchLimit < 12 {
				fetchLimit = 12
			}
			fetchOpts.Limit = fetchLimit

			if res.Input.Engine.pipeline != nil {
				if reranked, err := res.Input.Engine.pipeline.RecallWithEmbedding(ctx, res.Input.Query, fetchOpts, res.Input.Engine.embedder); err == nil && len(reranked) > 0 {
					merged = mergeHybridRRF([][]Record{merged, reranked}, []float64{0.35, 0.65}, res.Input.Opts.Limit)
				}
			}
		}

		return merged, nil
	})

	err := g.AddLambdaNode("rewrite", rewriteLambda)
	if err != nil {
		return nil, fmt.Errorf("failed to add rewrite node: %w", err)
	}

	err = g.AddLambdaNode("retrieve", retrieveLambda)
	if err != nil {
		return nil, fmt.Errorf("failed to add retrieve node: %w", err)
	}

	err = g.AddLambdaNode("merge", mergeLambda)
	if err != nil {
		return nil, fmt.Errorf("failed to add merge node: %w", err)
	}

	// Link START -> rewrite -> retrieve -> merge -> END
	if err = g.AddEdge(compose.START, "rewrite"); err != nil {
		return nil, fmt.Errorf("failed to link start to rewrite: %w", err)
	}
	if err = g.AddEdge("rewrite", "retrieve"); err != nil {
		return nil, fmt.Errorf("failed to link rewrite to retrieve: %w", err)
	}
	if err = g.AddEdge("retrieve", "merge"); err != nil {
		return nil, fmt.Errorf("failed to link retrieve to merge: %w", err)
	}
	if err = g.AddEdge("merge", compose.END); err != nil {
		return nil, fmt.Errorf("failed to link merge to end: %w", err)
	}

	return g.Compile(ctx)
}
