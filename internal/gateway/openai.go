package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// OpenAI-compatible /v1/chat/completions (stream + non-stream).

type oaiMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []part
}

type oaiChatRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	Stream      bool         `json:"stream"`
	Temperature *float64     `json:"temperature,omitempty"`
	User        string       `json:"user,omitempty"` // optional session hint
}

type oaiChatChoice struct {
	Index        int            `json:"index"`
	Message      *oaiMessage    `json:"message,omitempty"`
	Delta        *oaiDelta      `json:"delta,omitempty"`
	FinishReason *string        `json:"finish_reason"`
}

type oaiDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type oaiChatResponse struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Created int64           `json:"created"`
	Model   string          `json:"model"`
	Choices []oaiChatChoice `json:"choices"`
	Usage   *oaiUsage       `json:"usage,omitempty"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type oaiModelList struct {
	Object string      `json:"object"`
	Data   []oaiModel  `json:"data"`
}

type oaiModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	model := s.backend.ChatModelName()
	if model == "" {
		model = "agentgo"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(oaiModelList{
		Object: "list",
		Data: []oaiModel{{
			ID: model, Object: "model", Created: time.Now().Unix(), OwnedBy: "agentgo",
		}},
	})
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req oaiChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userText := extractOAIMessage(req.Messages)
	if userText == "" {
		http.Error(w, "no user message", http.StatusBadRequest)
		return
	}
	sessionID := strings.TrimSpace(req.User)
	if sessionID == "" {
		sessionID = strings.TrimSpace(r.Header.Get("X-Session-ID"))
	}
	model := req.Model
	if model == "" {
		model = s.backend.ChatModelName()
	}
	if model == "" {
		model = "agentgo"
	}

	chatReq := ChatRequest{SessionID: sessionID, Message: userText}
	ctx := r.Context()

	if req.Stream {
		swr, err := NewSSEWriter(w)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		id := "chatcmpl-" + uuid.NewString()
		created := time.Now().Unix()
		// role chunk first (OpenAI convention)
		_ = swr.WriteRaw([]byte(fmt.Sprintf(
			"data: %s\n\n", mustOAIChunkJSON(id, created, model, oaiDelta{Role: "assistant"}, nil),
		)))
		emit := func(_ string, data []byte) error {
			var payload struct {
				Delta string `json:"delta"`
			}
			_ = json.Unmarshal(data, &payload)
			if payload.Delta == "" {
				return nil
			}
			return swr.WriteRaw([]byte(fmt.Sprintf(
				"data: %s\n\n", mustOAIChunkJSON(id, created, model, oaiDelta{Content: payload.Delta}, nil),
			)))
		}
		res, err := s.backend.StreamChat(ctx, chatReq, emit)
		if err != nil {
			_ = swr.WriteRaw([]byte(fmt.Sprintf("data: %s\n\n", oaiErrorJSON(err.Error()))))
			return
		}
		fr := "stop"
		_ = swr.WriteRaw([]byte(fmt.Sprintf(
			"data: %s\n\n", mustOAIChunkJSON(id, created, model, oaiDelta{}, &fr),
		)))
		if res != nil && res.Content != "" {
			_ = res
		}
		_ = swr.WriteRaw([]byte("data: [DONE]\n\n"))
		return
	}

	// Non-streaming
	var content string
	res, err := s.backend.StreamChat(ctx, chatReq, func(_ string, data []byte) error {
		var payload struct {
			Delta string `json:"delta"`
		}
		_ = json.Unmarshal(data, &payload)
		content += payload.Delta
		return nil
	})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": err.Error(), "type": "server_error"},
		})
		return
	}
	if res != nil && res.Content != "" {
		content = res.Content
	}
	stop := "stop"
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(oaiChatResponse{
		ID: "chatcmpl-" + uuid.NewString(), Object: "chat.completion",
		Created: time.Now().Unix(), Model: model,
		Choices: []oaiChatChoice{{
			Index: 0,
			Message: &oaiMessage{Role: "assistant", Content: content},
			FinishReason: &stop,
		}},
		Usage: &oaiUsage{CompletionTokens: len(content) / 4, TotalTokens: len(content) / 4},
	})
}

func extractOAIMessage(msgs []oaiMessage) string {
	var parts []string
	for _, m := range msgs {
		if strings.ToLower(m.Role) != "user" {
			continue
		}
		switch c := m.Content.(type) {
		case string:
			if strings.TrimSpace(c) != "" {
				parts = append(parts, c)
			}
		case []any:
			for _, item := range c {
				if mp, ok := item.(map[string]any); ok {
					if t, _ := mp["text"].(string); t != "" {
						parts = append(parts, t)
					}
				}
			}
		}
	}
	if len(parts) == 0 && len(msgs) > 0 {
		// fallback: last message content
		last := msgs[len(msgs)-1]
		if s, ok := last.Content.(string); ok {
			return s
		}
	}
	return strings.Join(parts, "\n")
}

func mustOAIChunkJSON(id string, created int64, model string, delta oaiDelta, finish *string) string {
	ch := oaiChatChoice{Index: 0, Delta: &delta, FinishReason: finish}
	resp := oaiChatResponse{
		ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
		Choices: []oaiChatChoice{ch},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

func oaiErrorJSON(msg string) string {
	b, _ := json.Marshal(map[string]any{
		"error": map[string]any{"message": msg, "type": "server_error"},
	})
	return string(b)
}

// WriteRaw writes pre-formatted SSE lines (OpenAI data: format).
func (s *SSEWriter) WriteRaw(b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.w.Write(b)
	if err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}
