package im

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	channel "agentgo/internal/imchannel"
	"agentgo/internal/imchannel/adapters/weixin/ilink"
	"agentgo/internal/imchannel/datahome"
)

func adminSandbox(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	datahome.SetDir(dir)
	t.Cleanup(func() { datahome.SetDir("") })
	oldValidate := validateConfig
	validateConfig = func(platform string, cfg channel.PlatformConfig) []string {
		if platform == "telegram" {
			if tok, _ := cfg["bot_token"].(string); tok == "" {
				return []string{"bot_token is required"}
			}
		}
		return nil
	}
	oldCreds := hasWeixinCreds
	hasWeixinCreds = func() bool { return false }
	t.Cleanup(func() { validateConfig = oldValidate; hasWeixinCreds = oldCreds })
	return dir
}

func TestSaveChannelNeverReturnsSecretsAndKeepsThemWhenBlank(t *testing.T) {
	adminSandbox(t)
	err := SaveChannel(SaveRequest{Platform: "telegram", Enabled: true, AllowFrom: []string{"42, 43"}, Fields: map[string]string{"bot_token": "123:SECRET"}})
	if err != nil {
		t.Fatal(err)
	}
	st := ReadAdminStatus("")
	if len(st.Channels) != 1 {
		t.Fatalf("channels = %+v", st.Channels)
	}
	c := st.Channels[0]
	if !c.Enabled || !c.SecretsSet["bot_token"] || strings.Contains(strings.Join(c.AllowFrom, ","), "SECRET") {
		t.Fatalf("view = %+v", c)
	}
	if _, leaked := c.Fields["bot_token"]; leaked {
		t.Fatal("secret must not be in Fields")
	}
	if len(c.AllowFrom) != 2 {
		t.Fatalf("allow_from = %v", c.AllowFrom)
	}

	// 密钥留空 = 保持原值
	if err := SaveChannel(SaveRequest{Platform: "telegram", Enabled: true, AllowFrom: []string{"42"}, Fields: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	path, _ := channel.ConfigPath()
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "123:SECRET") {
		t.Fatalf("blank secret must keep the old value:\n%s", raw)
	}
	if got := ReadAdminStatus("").Channels[0].AllowFrom; len(got) != 1 || got[0] != "42" {
		t.Fatalf("allow_from not replaced: %v", got)
	}
}

func TestSaveChannelRejectsInvalidEnabledButAllowsDisabledDraft(t *testing.T) {
	adminSandbox(t)
	if err := SaveChannel(SaveRequest{Platform: "telegram", Enabled: true}); err == nil {
		t.Fatal("enabling without a token must fail")
	}
	if err := SaveChannel(SaveRequest{Platform: "telegram", Enabled: false, AllowFrom: []string{"1"}}); err != nil {
		t.Fatalf("a disabled draft is allowed: %v", err)
	}
	if err := SaveChannel(SaveRequest{Platform: "nope", Enabled: true}); err == nil {
		t.Fatal("unknown platform must fail")
	}
}

func TestRemoveChannel(t *testing.T) {
	adminSandbox(t)
	_ = SaveChannel(SaveRequest{Platform: "telegram", Enabled: true, AllowAll: true, Fields: map[string]string{"bot_token": "x"}})
	if err := RemoveChannel("telegram"); err != nil {
		t.Fatal(err)
	}
	if n := len(ReadAdminStatus("").Channels); n != 0 {
		t.Fatalf("channels left: %d", n)
	}
}

func TestReloadDefaultWithoutManager(t *testing.T) {
	SetDefault(nil)
	if _, err := ReloadDefault(); err == nil {
		t.Fatal("expected an error when IM is not running")
	}
}

func TestWeixinLoginFlowEnablesChannelForTheScanningAccount(t *testing.T) {
	adminSandbox(t)
	old := weixinLoginFn
	t.Cleanup(func() { weixinLoginFn = old })
	weixinLoginFn = func(ctx context.Context, o ilink.LoginOptions) (*ilink.Credentials, error) {
		o.OnQRURL("https://example.invalid/qr")
		o.OnScanned()
		return &ilink.Credentials{Token: "t", UserID: "wx-user-1"}, nil
	}
	StartWeixinLogin()
	deadline := time.Now().Add(3 * time.Second)
	var st LoginState
	for time.Now().Before(deadline) {
		st = WeixinLoginState()
		if st.Status == "done" || st.Status == "error" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.Status != "done" || st.UserID != "wx-user-1" {
		t.Fatalf("state = %+v", st)
	}
	ch := ReadAdminStatus("").Channels
	if len(ch) != 1 || ch[0].Platform != "weixin" || !ch[0].Enabled || len(ch[0].AllowFrom) != 1 || ch[0].AllowFrom[0] != "wx-user-1" {
		t.Fatalf("weixin channel = %+v", ch)
	}
}

func TestWeixinLoginQRIsAPNGDataURL(t *testing.T) {
	adminSandbox(t)
	old := weixinLoginFn
	t.Cleanup(func() { weixinLoginFn = old })
	got := make(chan struct{})
	weixinLoginFn = func(ctx context.Context, o ilink.LoginOptions) (*ilink.Credentials, error) {
		o.OnQRURL("https://example.invalid/qr")
		close(got)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	StartWeixinLogin()
	<-got
	st := WeixinLoginState()
	if !strings.HasPrefix(st.QRDataURL, "data:image/png;base64,") || st.Status != "waiting" {
		t.Fatalf("state = %+v", st)
	}
	if st := CancelWeixinLogin(); st.Status != "idle" {
		t.Fatalf("after cancel = %+v", st)
	}
}
