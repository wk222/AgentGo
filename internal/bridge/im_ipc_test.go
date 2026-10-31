package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/im"
)

// 设置页「聊天软件」：真实运行时里保存配置，管理层按新配置重连，密钥不回传。
func TestIMSettingsRoundTripOnARealRuntime(t *testing.T) {
	rt := boot(t)
	svc := &AppService{rt: rt}

	st := svc.GetIMStatus()
	if st.Disabled {
		t.Skipf("IM disabled in this environment: %s", st.DisabledReason)
	}
	if st.ConfigPath != filepath.Join(rt.dataDir, "channels.yml") {
		t.Fatalf("config path = %q", st.ConfigPath)
	}
	if len(st.Platforms) < 6 || st.Channels == nil {
		t.Fatalf("status = %+v", st)
	}

	// 没填密钥就启用：拒绝，且不写文件。
	if _, err := svc.SaveIMChannel(im.SaveRequest{Platform: "telegram", Enabled: true, AllowFrom: []string{"1"}}); err == nil {
		t.Fatal("enabling telegram without a token must fail")
	}
	if _, err := os.Stat(st.ConfigPath); err == nil {
		t.Fatal("a rejected save must not create channels.yml")
	}

	// 草稿（未启用）可以保存，管理层不会去连接。
	got, err := svc.SaveIMChannel(im.SaveRequest{
		Platform: "telegram", Enabled: false, AllowFrom: []string{"1", "2"}, Fields: map[string]string{"bot_token": "111:SECRET"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Channels) != 1 || got.Channels[0].Running || !got.Channels[0].SecretsSet["bot_token"] {
		t.Fatalf("after save: %+v", got.Channels)
	}
	raw, _ := os.ReadFile(got.ConfigPath)
	if !strings.Contains(string(raw), "111:SECRET") {
		t.Fatalf("the secret must be stored in the file:\n%s", raw)
	}

	got, err = svc.RemoveIMChannel("telegram")
	if err != nil || len(got.Channels) != 0 {
		t.Fatalf("remove: %v %+v", err, got.Channels)
	}
}
