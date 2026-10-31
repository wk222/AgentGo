package im

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	channel "agentgo/internal/imchannel"
)

// 设置页对渠道的读写：channels.yml 仍是唯一的配置来源，这里只是替用户读它、改它。
// 密钥（token/secret）只告诉界面“已设置”，永远不把值发回去。

// FieldInfo 描述一个平台的一项配置。
type FieldInfo struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
	Hint   string `json:"hint,omitempty"`
}

// PlatformInfo 描述一个可配置的平台。
type PlatformInfo struct {
	ID      string      `json:"id"`
	Label   string      `json:"label"`
	Fields  []FieldInfo `json:"fields"`
	QRLogin bool        `json:"qr_login"` // 需要扫码登录（微信）
	Help    string      `json:"help,omitempty"`
}

// platformTable 是界面能配置的平台。字段名必须和适配器读取的 key 一致。
var platformTable = []PlatformInfo{
	{ID: "telegram", Label: "Telegram", Fields: []FieldInfo{
		{Key: "bot_token", Label: "Bot Token", Secret: true, Hint: "在 @BotFather 创建机器人后得到"},
	}, Help: "用户 ID 可以先留空，给机器人发一条消息，它会回复你的 ID。"},
	{ID: "feishu", Label: "飞书", Fields: []FieldInfo{
		{Key: "app_id", Label: "App ID"},
		{Key: "app_secret", Label: "App Secret", Secret: true},
	}},
	{ID: "dingtalk", Label: "钉钉", Fields: []FieldInfo{
		{Key: "client_id", Label: "Client ID"},
		{Key: "client_secret", Label: "Client Secret", Secret: true},
	}},
	{ID: "wecom", Label: "企业微信", Fields: []FieldInfo{
		{Key: "bot_id", Label: "智能机器人 Bot ID"},
		{Key: "secret", Label: "智能机器人 Secret", Secret: true},
		{Key: "webhook_url", Label: "群机器人 Webhook 地址", Hint: "只想主动推送消息时填这一项（或下面的 key）"},
		{Key: "webhook_key", Label: "群机器人 Webhook Key", Secret: true},
	}},
	{ID: "discord", Label: "Discord", Fields: []FieldInfo{
		{Key: "bot_token", Label: "Bot Token", Secret: true},
	}},
	{ID: "weixin", Label: "微信（iLink）", QRLogin: true, Help: "不需要填密钥：点「扫码登录」，用微信扫二维码确认。"},
}

// Platforms 返回界面可配置的平台表。
func Platforms() []PlatformInfo {
	out := make([]PlatformInfo, len(platformTable))
	copy(out, platformTable)
	return out
}

func findPlatform(id string) (PlatformInfo, bool) {
	for _, p := range platformTable {
		if p.ID == id {
			return p, true
		}
	}
	return PlatformInfo{}, false
}

// ChannelView 是界面看到的一个渠道（只含第一个实例）。
type ChannelView struct {
	Platform   string            `json:"platform"`
	Enabled    bool              `json:"enabled"`
	Running    bool              `json:"running"`
	AllowAll   bool              `json:"allow_all"`
	AllowFrom  []string          `json:"allow_from"`
	Fields     map[string]string `json:"fields"`      // 非密钥配置的当前值
	SecretsSet map[string]bool   `json:"secrets_set"` // 哪些密钥已经填过（不含值）
	LoggedIn   bool              `json:"logged_in"`   // 微信：已有登录凭据
	Problems   []string          `json:"problems,omitempty"`
}

// AdminStatus 是设置页「聊天软件」卡片的数据。
type AdminStatus struct {
	ConfigPath     string         `json:"config_path"`
	Disabled       bool           `json:"disabled"`
	DisabledReason string         `json:"disabled_reason,omitempty"`
	Platforms      []PlatformInfo `json:"platforms"`
	Channels       []ChannelView  `json:"channels"`
	Error          string         `json:"error,omitempty"`
}

// 单测可以替换。
var (
	hasWeixinCreds = weixinHasCredentials
	validateConfig = func(platform string, cfg channel.PlatformConfig) []string {
		ad, err := registryFactory(platform, cfg)
		if err != nil {
			return []string{err.Error()}
		}
		return ad.ValidateConfig(cfg)
	}
)

// ReadAdminStatus 读 channels.yml 并结合运行中的管理层，生成界面数据。
func ReadAdminStatus(disabledReason string) AdminStatus {
	st := AdminStatus{Platforms: Platforms(), Channels: []ChannelView{}, DisabledReason: disabledReason, Disabled: disabledReason != ""}
	if p, err := channel.ConfigPath(); err == nil {
		st.ConfigPath = p
	}
	cfg, err := channel.LoadConfig()
	if err != nil {
		st.Error = "channels.yml 读取失败：" + err.Error()
		return st
	}
	running := map[string]bool{}
	if m := Default(); m != nil {
		for _, id := range m.RunningIDs() {
			running[id] = true
		}
	}
	names := make([]string, 0, len(cfg.Channels))
	for name := range cfg.Channels {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		list := cfg.Channels[name]
		if len(list) == 0 {
			continue
		}
		ref := channel.InstanceRef{Platform: name, Instance: list[0]}
		st.Channels = append(st.Channels, viewOf(ref, running[ref.AdapterID()]))
	}
	return st
}

func viewOf(ref channel.InstanceRef, running bool) ChannelView {
	v := ChannelView{
		Platform: ref.Platform, Enabled: ref.Instance.Enabled, Running: running,
		AllowFrom: []string{}, Fields: map[string]string{}, SecretsSet: map[string]bool{},
	}
	cfg := ref.Instance.Config
	acc := ParseAccess(cfg)
	v.AllowAll = acc.AllowAll
	for k := range acc.AllowFrom {
		v.AllowFrom = append(v.AllowFrom, k)
	}
	sort.Strings(v.AllowFrom)
	if p, ok := findPlatform(ref.Platform); ok {
		for _, f := range p.Fields {
			s, _ := cfg[f.Key].(string)
			if f.Secret {
				v.SecretsSet[f.Key] = s != ""
			} else {
				v.Fields[f.Key] = s
			}
		}
		if p.QRLogin {
			v.LoggedIn = hasWeixinCreds()
		}
	}
	if ref.Instance.Enabled {
		v.Problems = validateConfig(ref.Platform, cfg)
	}
	return v
}

// SaveRequest 是界面提交的一个渠道。
type SaveRequest struct {
	Platform  string            `json:"platform"`
	Enabled   bool              `json:"enabled"`
	AllowAll  bool              `json:"allow_all"`
	AllowFrom []string          `json:"allow_from"`
	Fields    map[string]string `json:"fields"` // 密钥留空表示保持原值；非密钥留空表示清除
}

// SaveChannel 把一个渠道写进 channels.yml。启用时先校验配置，不合格就不写。
func SaveChannel(req SaveRequest) error {
	p, ok := findPlatform(strings.TrimSpace(req.Platform))
	if !ok {
		return fmt.Errorf("不支持的平台：%q", req.Platform)
	}
	cfg, err := channel.LoadConfig()
	if err != nil {
		return err
	}
	allow := cleanList(req.AllowFrom)
	merged := channel.PlatformConfig{}
	if list := cfg.Channels[p.ID]; len(list) > 0 {
		for k, v := range list[0].Config {
			merged[k] = v
		}
	}
	for _, f := range p.Fields {
		val := strings.TrimSpace(req.Fields[f.Key])
		switch {
		case val != "":
			merged[f.Key] = val
		case f.Secret: // 保持原值
		default:
			delete(merged, f.Key)
		}
	}
	merged["allow_all"] = req.AllowAll
	if len(allow) > 0 {
		merged["allow_from"] = allow
	} else {
		delete(merged, "allow_from")
	}
	if req.Enabled {
		if errs := validateConfig(p.ID, merged); len(errs) > 0 {
			return errors.New(strings.Join(errs, "；"))
		}
	}
	// SetPlatform 是“合并”，要清掉的键先在原配置里删掉。
	if list := cfg.Channels[p.ID]; len(list) > 0 && list[0].Config != nil {
		for k := range list[0].Config {
			if _, keep := merged[k]; !keep {
				delete(list[0].Config, k)
			}
		}
	}
	fields := map[string]any{}
	for k, v := range merged {
		fields[k] = v
	}
	fields["enabled"] = req.Enabled
	cfg.SetPlatform(p.ID, fields)
	return cfg.Save()
}

// RemoveChannel 从 channels.yml 删掉一个平台。
func RemoveChannel(platform string) error {
	cfg, err := channel.LoadConfig()
	if err != nil {
		return err
	}
	cfg.RemovePlatform(strings.TrimSpace(platform))
	return cfg.Save()
}

// ReloadDefault 让运行中的管理层按当前 channels.yml 重新连接。返回连上的实例数。
func ReloadDefault() (int, error) {
	m := Default()
	if m == nil {
		return 0, errors.New("聊天软件功能没有运行（AGENTGO_DISABLE_IM=1，或插件已停用）")
	}
	return m.Reload()
}

func cleanList(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == ' ' }) {
			if part = strings.TrimSpace(part); part != "" && !seen[part] {
				seen[part] = true
				out = append(out, part)
			}
		}
	}
	return out
}
