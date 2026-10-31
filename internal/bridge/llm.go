package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/config"
)

// APITestResult is returned to the frontend after probing the OpenAI-compatible endpoint.
type APITestResult struct {
	OK       bool     `json:"ok"`
	Message  string   `json:"message"`
	Models   []string `json:"models,omitempty"`
	HTTPCode int      `json:"http_code,omitempty"`
}

// TestLLMConnection checks reachability of the configured api_base (like PyBot Settings test).
func TestLLMConnection(ctx context.Context, cfg LLMConfig) APITestResult {
	if strings.TrimSpace(cfg.APIBase) == "" {
		return APITestResult{OK: false, Message: "api_base 未配置"}
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return APITestResult{OK: false, Message: "api_key 未配置（可设置环境变量 AGENTGO_API_KEY 或桌面端设置）"}
	}

	base := strings.TrimRight(cfg.APIBase, "/")
	// A local /models endpoint can be healthy even when upstream authentication
	// is broken. Verify a real, bounded chat request instead.
	payload, _ := json.Marshal(map[string]any{"model": cfg.Model, "messages": []map[string]string{{"role": "user", "content": "Reply with OK."}}, "stream": true, "max_tokens": 256})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatCompletionURL(base), bytes.NewReader(payload))
	if err != nil {
		return APITestResult{OK: false, Message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("api-key", cfg.APIKey)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return APITestResult{OK: false, Message: "模型连接失败或等待首个响应超时，请检查网络和 API 地址"}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		message := fmt.Sprintf("模型请求失败（HTTP %d）", resp.StatusCode)
		if resp.StatusCode == 401 {
			message = "模型鉴权失败（401），请重新登录账号或检查 API Key"
		}
		var envelope struct{ Error struct{ Message string } }
		if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
			message = envelope.Error.Message
		}
		return APITestResult{OK: false, Message: message, HTTPCode: resp.StatusCode}
	}
	// Stop at the first valid streamed choice; do not wait for the entire
	// generation or require non-streaming support from the gateway.
	valid := func(body []byte) bool {
		var parsed struct{ Choices []json.RawMessage }
		return json.Unmarshal(body, &parsed) == nil && len(parsed.Choices) > 0
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				break
			}
			if valid([]byte(data)) {
				return APITestResult{OK: true, Message: "模型鉴权和流式响应已验证", HTTPCode: resp.StatusCode}
			}
		}
	} else {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if valid(body) {
			return APITestResult{OK: true, Message: "模型鉴权和实际响应已验证", HTTPCode: resp.StatusCode}
		}
	}
	return APITestResult{OK: false, Message: "网关未返回有效的模型响应", HTTPCode: resp.StatusCode}

}

// ChatOnce calls the configured model (OpenAI-compatible).
func ChatOnce(ctx context.Context, cfg LLMConfig, systemPrompt, userMessage string) (string, error) {
	if cfg.APIKey == "" {
		return "", fmt.Errorf("未配置 API Key")
	}
	modelName := cfg.Model
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}

	chatCfg := &openai.ChatModelConfig{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.APIBase,
		Model:   modelName,
	}
	if v := config.ReasoningEffort.Get(); v != "" {
		chatCfg.ReasoningEffort = openai.ReasoningEffortLevel(v)
	} else if strings.Contains(strings.ToLower(modelName), "luna") || strings.Contains(strings.ToLower(modelName), "gpt-5") {
		chatCfg.ReasoningEffort = openai.ReasoningEffortLevel("none")
	}

	cm, err := openai.NewChatModel(ctx, chatCfg)
	if err != nil {
		return "", err
	}

	msgs := []*schema.Message{}
	if strings.TrimSpace(systemPrompt) != "" {
		msgs = append(msgs, schema.SystemMessage(systemPrompt))
	}
	msgs = append(msgs, schema.UserMessage(userMessage))

	started := time.Now()
	out, err := cm.Generate(ctx, msgs)
	if err != nil {
		return "", err
	}
	if out == nil {
		return "", fmt.Errorf("empty model response")
	}
	recordChatUsage(modelName, out, time.Since(started))
	return out.Content, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// quickChatProbe uses raw HTTP for minimal dependency when eino model fails.
func quickChatProbe(ctx context.Context, cfg LLMConfig, user string) (string, error) {
	base := strings.TrimRight(cfg.APIBase, "/")
	payload := map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "user", "content": user},
		},
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("api-key", cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("chat HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return parsed.Choices[0].Message.Content, nil
}

func chatCompletionURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}
