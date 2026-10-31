package channels

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Supported channel adapter kinds.
const (
	KindWebhook  = "webhook"
	KindFeishu   = "feishu"
	KindDingTalk = "dingtalk"
	KindWeCom    = "wecom"
	KindTelegram = "telegram"
)

// ChannelSpec defines a modular, hot-pluggable channel adapter.
type ChannelSpec struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind"` // webhook | feishu | dingtalk | wecom | telegram
	Label      string            `json:"label,omitempty"`
	Enabled    bool              `json:"enabled"`
	WebhookURL string            `json:"webhook_url,omitempty"`
	Secret     string            `json:"secret,omitempty"`    // HMAC signing secret (Feishu / DingTalk / Webhook)
	BotToken   string            `json:"bot_token,omitempty"` // Telegram bot token
	ChatID     string            `json:"chat_id,omitempty"`   // Telegram target chat_id
	Headers    map[string]string `json:"headers,omitempty"`
	Note       string            `json:"note,omitempty"`
}

// OutboundEnvelope is the built HTTP request for an outbound channel message.
type OutboundEnvelope struct {
	Channel string            `json:"channel"`
	Kind    string            `json:"kind"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

// SendResult is returned by Hub.Send.
type SendResult struct {
	Channel    string            `json:"channel"`
	Kind       string            `json:"kind"`
	DryRun     bool              `json:"dry_run"`
	StatusCode int               `json:"status_code,omitempty"`
	Response   string            `json:"response,omitempty"`
	Envelope   *OutboundEnvelope `json:"envelope,omitempty"`
}

// InboundMessage is the normalized message extracted from an external channel webhook.
type InboundMessage struct {
	Channel   string `json:"channel"`
	Kind      string `json:"kind"`
	Sender    string `json:"sender,omitempty"`
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
	Challenge string `json:"challenge,omitempty"` // URL verification challenge (e.g. Feishu)
}

// Hub manages dynamic channel adapters persisted in a JSON file.
type Hub struct {
	mu       sync.RWMutex
	filePath string
	specs    map[string]ChannelSpec
	client   *http.Client
}

// NewHub creates a channel hub backed by filePath (if non-empty).
func NewHub(filePath string) *Hub {
	h := &Hub{
		filePath: filePath,
		specs:    make(map[string]ChannelSpec),
		client:   &http.Client{Timeout: 15 * time.Second},
	}
	if filePath != "" {
		_ = h.Load()
	}
	return h
}

// Load reads channel specs from disk if the file exists.
func (h *Hub) Load() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.filePath == "" {
		return nil
	}
	data, err := os.ReadFile(h.filePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var list []ChannelSpec
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	h.specs = make(map[string]ChannelSpec, len(list))
	for _, s := range list {
		h.specs[s.Name] = s
	}
	return nil
}

func (h *Hub) saveLocked() error {
	if h.filePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(h.filePath), 0o755); err != nil {
		return err
	}
	list := h.listLocked()
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(h.filePath, b, 0o644)
}

func (h *Hub) listLocked() []ChannelSpec {
	out := make([]ChannelSpec, 0, len(h.specs))
	for _, s := range h.specs {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// List returns all registered channel specs sorted by name.
func (h *Hub) List() []ChannelSpec {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.listLocked()
}

// Statuses returns the combined view of default and dynamically registered channels.
func (h *Hub) Statuses() []ChannelStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := DefaultStatuses()
	for _, s := range h.listLocked() {
		label := s.Label
		if label == "" {
			label = s.Name + " (" + s.Kind + ")"
		}
		out = append(out, ChannelStatus{
			Kind:    s.Kind,
			Enabled: s.Enabled,
			Label:   label,
			Note:    s.Note,
		})
	}
	return out
}

// Upsert validates, registers and persists a channel adapter.
func (h *Hub) Upsert(s ChannelSpec) (ChannelSpec, error) {
	s.Name = strings.TrimSpace(s.Name)
	s.Kind = strings.ToLower(strings.TrimSpace(s.Kind))
	if s.Name == "" {
		return ChannelSpec{}, fmt.Errorf("channel name is required")
	}
	switch s.Kind {
	case KindWebhook, KindFeishu, KindDingTalk, KindWeCom:
		if strings.TrimSpace(s.WebhookURL) == "" {
			return ChannelSpec{}, fmt.Errorf("webhook_url is required for kind=%s", s.Kind)
		}
	case KindTelegram:
		if strings.TrimSpace(s.WebhookURL) == "" && (strings.TrimSpace(s.BotToken) == "" || strings.TrimSpace(s.ChatID) == "") {
			return ChannelSpec{}, fmt.Errorf("telegram requires bot_token + chat_id (or custom webhook_url)")
		}
	default:
		return ChannelSpec{}, fmt.Errorf("unsupported channel kind %q (want webhook|feishu|dingtalk|wecom|telegram)", s.Kind)
	}
	if s.Label == "" {
		s.Label = s.Name
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.specs[s.Name] = s
	if err := h.saveLocked(); err != nil {
		return ChannelSpec{}, err
	}
	return s, nil
}

// Remove deletes a channel adapter by name.
func (h *Hub) Remove(name string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	name = strings.TrimSpace(name)
	if _, ok := h.specs[name]; !ok {
		return false, nil
	}
	delete(h.specs, name)
	return true, h.saveLocked()
}

// BuildOutbound constructs the signed protocol-specific request for a channel at time now.
func (h *Hub) BuildOutbound(name, title, text string, now time.Time) (*OutboundEnvelope, error) {
	h.mu.RLock()
	spec, ok := h.specs[strings.TrimSpace(name)]
	h.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("channel %q not found", name)
	}
	if !spec.Enabled {
		return nil, fmt.Errorf("channel %q is disabled", name)
	}

	headers := map[string]string{"Content-Type": "application/json; charset=utf-8"}
	for k, v := range spec.Headers {
		headers[k] = v
	}
	targetURL := spec.WebhookURL
	fullText := text
	if strings.TrimSpace(title) != "" {
		fullText = "【" + strings.TrimSpace(title) + "】\n" + text
	}

	var payload any
	switch spec.Kind {
	case KindFeishu:
		body := map[string]any{
			"msg_type": "text",
			"content":  map[string]string{"text": fullText},
		}
		if spec.Secret != "" {
			ts := strconv.FormatInt(now.Unix(), 10)
			// Feishu v2 bot signature: HMAC-SHA256 with key = timestamp + "\n" + secret, empty message.
			stringToSign := ts + "\n" + spec.Secret
			mac := hmac.New(sha256.New, []byte(stringToSign))
			body["timestamp"] = ts
			body["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
		}
		payload = body

	case KindDingTalk:
		if spec.Secret != "" {
			ts := strconv.FormatInt(now.UnixMilli(), 10)
			stringToSign := ts + "\n" + spec.Secret
			mac := hmac.New(sha256.New, []byte(spec.Secret))
			_, _ = mac.Write([]byte(stringToSign))
			sign := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
			sep := "?"
			if strings.Contains(targetURL, "?") {
				sep = "&"
			}
			targetURL = fmt.Sprintf("%s%stimestamp=%s&sign=%s", targetURL, sep, ts, sign)
		}
		if strings.TrimSpace(title) != "" {
			payload = map[string]any{
				"msgtype":  "markdown",
				"markdown": map[string]string{"title": title, "text": "### " + title + "\n\n" + text},
			}
		} else {
			payload = map[string]any{
				"msgtype": "text",
				"text":    map[string]string{"content": text},
			}
		}

	case KindWeCom:
		payload = map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]string{"content": fullText},
		}

	case KindTelegram:
		if targetURL == "" {
			targetURL = fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", spec.BotToken)
		}
		payload = map[string]any{
			"chat_id": spec.ChatID,
			"text":    fullText,
		}

	default: // KindWebhook
		payload = map[string]any{
			"channel":   spec.Name,
			"title":     title,
			"text":      text,
			"timestamp": now.UTC().Format(time.RFC3339),
		}
	}

	rawBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if spec.Kind == KindWebhook && spec.Secret != "" {
		mac := hmac.New(sha256.New, []byte(spec.Secret))
		_, _ = mac.Write(rawBody)
		headers["X-AgentGo-Signature"] = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	return &OutboundEnvelope{
		Channel: spec.Name,
		Kind:    spec.Kind,
		URL:     redactURLToken(targetURL, spec.BotToken),
		Headers: headers,
		Body:    rawBody,
	}, nil
}

func redactURLToken(u, token string) string {
	if token != "" && len(token) > 6 {
		return strings.ReplaceAll(u, token, token[:3]+"***"+token[len(token)-3:])
	}
	return u
}

// Send dispatches a message to the named channel (or returns the built envelope when dryRun is true).
func (h *Hub) Send(ctx context.Context, name, title, text string, dryRun bool) (*SendResult, error) {
	now := time.Now()
	env, err := h.BuildOutbound(name, title, text, now)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &SendResult{
			Channel:  env.Channel,
			Kind:     env.Kind,
			DryRun:   true,
			Envelope: env,
		}, nil
	}

	// Use unredacted URL for actual HTTP dispatch.
	h.mu.RLock()
	spec := h.specs[strings.TrimSpace(name)]
	h.mu.RUnlock()
	realURL := env.URL
	if spec.Kind == KindTelegram && spec.WebhookURL == "" {
		realURL = fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", spec.BotToken)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, realURL, bytes.NewReader(env.Body))
	if err != nil {
		return nil, err
	}
	for k, v := range env.Headers {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return &SendResult{
		Channel:    env.Channel,
		Kind:       env.Kind,
		StatusCode: resp.StatusCode,
		Response:   strings.TrimSpace(string(respBytes)),
		Envelope:   env,
	}, nil
}

// ParseInbound normalizes an incoming webhook payload from Feishu, DingTalk, WeCom, Telegram or generic JSON.
func (h *Hub) ParseInbound(name string, raw []byte) (*InboundMessage, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("invalid inbound JSON: %w", err)
	}
	kind := KindWebhook
	h.mu.RLock()
	if spec, ok := h.specs[strings.TrimSpace(name)]; ok {
		kind = spec.Kind
	}
	h.mu.RUnlock()

	msg := &InboundMessage{
		Channel:   name,
		Kind:      kind,
		SessionID: "channel:" + name,
	}
	if ch, ok := m["challenge"].(string); ok && ch != "" {
		msg.Challenge = ch
		return msg, nil
	}
	if sid, ok := m["session_id"].(string); ok && sid != "" {
		msg.SessionID = sid
	}

	// Probe common nested text locations across Feishu v2, DingTalk, Telegram, WeCom, and generic webhooks.
	candidates := []string{
		strAt(m, "text"),
		strAt(m, "content"),
		strAt(m, "prompt"),
		strAt(m, "text", "content"),             // DingTalk / WeCom
		strAt(m, "message", "text"),             // Telegram
		strAt(m, "event", "message", "content"), // Feishu v2 (often a JSON string {"text":"..."})
	}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if strings.HasPrefix(c, "{") {
			var inner map[string]any
			if json.Unmarshal([]byte(c), &inner) == nil {
				if t := strings.TrimSpace(strAt(inner, "text")); t != "" {
					c = t
				}
			}
		}
		msg.Text = c
		break
	}
	msg.Sender = firstNonEmpty(
		strAt(m, "sender"),
		strAt(m, "senderNick"),
		strAt(m, "message", "from", "username"),
		strAt(m, "event", "sender", "sender_id", "open_id"),
	)
	if msg.Text == "" && msg.Challenge == "" {
		return nil, fmt.Errorf("no message text found in inbound payload")
	}
	return msg, nil
}

func strAt(m map[string]any, path ...string) string {
	cur := any(m)
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = obj[p]
	}
	s, _ := cur.(string)
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
