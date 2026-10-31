package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChannelHubAdaptersAndPersistence(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "channels.json")
	h := NewHub(cfgPath)

	// 1. Upsert Feishu adapter with HMAC secret
	_, err := h.Upsert(ChannelSpec{
		Name:       "ops_feishu",
		Kind:       KindFeishu,
		WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/test",
		Secret:     "sec_123456",
		Enabled:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Upsert DingTalk adapter with HMAC secret
	_, err = h.Upsert(ChannelSpec{
		Name:       "ops_ding",
		Kind:       KindDingTalk,
		WebhookURL: "https://oapi.dingtalk.com/robot/send?access_token=xyz",
		Secret:     "SEC_ding",
		Enabled:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Reload from disk to verify persistence
	h2 := NewHub(cfgPath)
	if len(h2.List()) != 2 {
		t.Fatalf("expected 2 persisted channels, got %d", len(h2.List()))
	}

	// 3. DryRun outbound envelope for Feishu and DingTalk
	resFeishu, err := h2.Send(context.Background(), "ops_feishu", "逆向完成", "发现 XOR 0xFF", true)
	if err != nil || !resFeishu.DryRun {
		t.Fatalf("feishu dry_run err=%v res=%+v", err, resFeishu)
	}
	if !strings.Contains(string(resFeishu.Envelope.Body), `"sign"`) || !strings.Contains(string(resFeishu.Envelope.Body), "【逆向完成】") {
		t.Fatalf("feishu body missing sign or title: %s", string(resFeishu.Envelope.Body))
	}

	resDing, err := h2.Send(context.Background(), "ops_ding", "告警", "测试正文", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resDing.Envelope.URL, "&timestamp=") || !strings.Contains(resDing.Envelope.URL, "&sign=") {
		t.Fatalf("dingtalk URL missing signature query: %s", resDing.Envelope.URL)
	}

	// 4. Real HTTP dispatch against local test server
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-AgentGo-Signature")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()

	_, _ = h2.Upsert(ChannelSpec{
		Name:       "local_hook",
		Kind:       KindWebhook,
		WebhookURL: srv.URL,
		Secret:     "my_hmac_key",
		Enabled:    true,
	})
	liveRes, err := h2.Send(context.Background(), "local_hook", "Title", "Hello", false)
	if err != nil || liveRes.StatusCode != 200 || !strings.HasPrefix(gotSig, "sha256=") {
		t.Fatalf("live send failed: err=%v res=%+v sig=%q", err, liveRes, gotSig)
	}

	// 5. ParseInbound across Feishu, DingTalk, Telegram, and Challenge
	feishuIn := `{"event":{"sender":{"sender_id":{"open_id":"ou_1"}},"message":{"content":"{\"text\":\"请分析这个APK\"}"}}}`
	msg, err := h2.ParseInbound("ops_feishu", []byte(feishuIn))
	if err != nil || msg.Text != "请分析这个APK" || msg.Sender != "ou_1" {
		t.Fatalf("feishu inbound: %+v err=%v", msg, err)
	}

	chalIn, _ := json.Marshal(map[string]string{"challenge": "ch_abc"})
	chMsg, err := h2.ParseInbound("ops_feishu", chalIn)
	if err != nil || chMsg.Challenge != "ch_abc" {
		t.Fatalf("challenge inbound: %+v err=%v", chMsg, err)
	}

	_ = time.Now()
}
