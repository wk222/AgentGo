package bridge

import (
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
)

// APITestResult is returned to the frontend after probing the OpenAI-compatible endpoint.
type APITestResult struct {
	OK       bool   `json:"ok"`
	Message  string `json:"message"`
	Models   []string `json:"models,omitempty"`
	HTTPCode int    `json:"http_code,omitempty"`
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
	url := base + "/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return APITestResult{OK: false, Message: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return APITestResult{OK: false, Message: "无法连接: " + err.Error()}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return APITestResult{
			OK:       false,
			Message:  "鉴权失败，请检查 API Key",
			HTTPCode: resp.StatusCode,
		}
	}
	if resp.StatusCode >= 400 {
		return APITestResult{
			OK:       false,
			Message:  truncate(string(body), 300),
			HTTPCode: resp.StatusCode,
		}
	}

	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	names := []string{}
	if json.Unmarshal(body, &parsed) == nil {
		for _, m := range parsed.Data {
			if m.ID != "" {
				names = append(names, m.ID)
			}
		}
	}

	return APITestResult{
		OK:      true,
		Message: fmt.Sprintf("连接成功 (HTTP %d)", resp.StatusCode),
		Models:  names,
		HTTPCode: resp.StatusCode,
	}
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

	cm, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.APIBase,
		Model:   modelName,
	})
	if err != nil {
		return "", err
	}

	msgs := []*schema.Message{}
	if strings.TrimSpace(systemPrompt) != "" {
		msgs = append(msgs, schema.SystemMessage(systemPrompt))
	}
	msgs = append(msgs, schema.UserMessage(userMessage))

	out, err := cm.Generate(ctx, msgs)
	if err != nil {
		return "", err
	}
	if out == nil {
		return "", fmt.Errorf("empty model response")
	}
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
