package im

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"

	"github.com/skip2/go-qrcode"

	channel "agentgo/internal/imchannel"
	"agentgo/internal/imchannel/adapters/weixin/ilink"
)

// 微信（iLink）要扫码登录：界面点「扫码登录」后，这里在后台跑 iLink 的登录流程，
// 把二维码（PNG data URL）和进度交给界面轮询。登录成功后凭据存到数据目录，
// 并把 weixin 渠道写进 channels.yml（默认只允许扫码的这个微信号），再让管理层重连。

// LoginState 是扫码登录的进度。
type LoginState struct {
	Status    string `json:"status"` // idle / waiting / scanned / expired / done / error
	QRDataURL string `json:"qr_data_url,omitempty"`
	Message   string `json:"message,omitempty"`
	UserID    string `json:"user_id,omitempty"`
}

type weixinLogin struct {
	mu     sync.Mutex
	state  LoginState
	cancel context.CancelFunc
	gen    int
}

var wxLogin = &weixinLogin{state: LoginState{Status: "idle"}}

// 单测可以替换：真实实现走 iLink HTTP 协议。
var weixinLoginFn = func(ctx context.Context, opts ilink.LoginOptions) (*ilink.Credentials, error) {
	return ilink.Login(ctx, ilink.NewClient(), opts)
}

func weixinHasCredentials() bool {
	c, err := ilink.LoadCredentials("")
	return err == nil && c != nil && c.Token != ""
}

// WeixinLoginState 返回当前登录进度。
func WeixinLoginState() LoginState {
	wxLogin.mu.Lock()
	defer wxLogin.mu.Unlock()
	return wxLogin.state
}

// CancelWeixinLogin 中止进行中的登录。
func CancelWeixinLogin() LoginState {
	wxLogin.mu.Lock()
	defer wxLogin.mu.Unlock()
	if wxLogin.cancel != nil {
		wxLogin.cancel()
		wxLogin.cancel = nil
	}
	wxLogin.gen++
	if s := wxLogin.state.Status; s == "waiting" || s == "scanned" || s == "expired" {
		wxLogin.state = LoginState{Status: "idle", Message: "已取消"}
	}
	return wxLogin.state
}

// StartWeixinLogin 开始（或重新开始）扫码登录，立即返回；进度用 WeixinLoginState 轮询。
func StartWeixinLogin() LoginState {
	wxLogin.mu.Lock()
	if wxLogin.cancel != nil {
		wxLogin.cancel()
	}
	wxLogin.gen++
	gen := wxLogin.gen
	ctx, cancel := context.WithCancel(context.Background())
	wxLogin.cancel = cancel
	wxLogin.state = LoginState{Status: "waiting", Message: "正在获取二维码…"}
	st := wxLogin.state
	wxLogin.mu.Unlock()

	set := func(s LoginState) {
		wxLogin.mu.Lock()
		defer wxLogin.mu.Unlock()
		if wxLogin.gen == gen { // 已被取消或重新开始的旧流程不再写状态
			wxLogin.state = s
		}
	}
	go func() {
		defer cancel()
		creds, err := weixinLoginFn(ctx, ilink.LoginOptions{
			Force: true,
			OnQRURL: func(url string) {
				png, qerr := qrcode.Encode(url, qrcode.Medium, 280)
				if qerr != nil {
					set(LoginState{Status: "error", Message: "生成二维码失败：" + qerr.Error()})
					return
				}
				set(LoginState{Status: "waiting", Message: "请用微信扫描二维码",
					QRDataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)})
			},
			OnScanned: func() {
				wxLogin.mu.Lock()
				qr := wxLogin.state.QRDataURL
				wxLogin.mu.Unlock()
				set(LoginState{Status: "scanned", Message: "已扫码，请在手机上确认", QRDataURL: qr})
			},
			OnExpired: func() { set(LoginState{Status: "expired", Message: "二维码过期，正在换一个…"}) },
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			set(LoginState{Status: "error", Message: err.Error()})
			return
		}
		if err := enableWeixinChannel(creds.UserID); err != nil {
			set(LoginState{Status: "error", Message: "登录成功，但写入配置失败：" + err.Error()})
			return
		}
		msg := "登录成功"
		if m := Default(); m != nil {
			if _, rerr := m.Reload(); rerr != nil {
				msg += "，但重新连接失败：" + rerr.Error()
			}
		}
		set(LoginState{Status: "done", Message: msg, UserID: creds.UserID})
	}()
	return st
}

// enableWeixinChannel 登录成功后启用 weixin 渠道。已有白名单时保留；没有时只放行扫码的这个微信号。
func enableWeixinChannel(userID string) error {
	cfg, err := channel.LoadConfig()
	if err != nil {
		return err
	}
	req := SaveRequest{Platform: "weixin", Enabled: true}
	if list := cfg.Channels["weixin"]; len(list) > 0 {
		acc := ParseAccess(list[0].Config)
		req.AllowAll = acc.AllowAll
		for k := range acc.AllowFrom {
			req.AllowFrom = append(req.AllowFrom, k)
		}
	}
	if !req.AllowAll && len(req.AllowFrom) == 0 {
		if userID == "" {
			return fmt.Errorf("登录凭据里没有用户 ID，无法设置白名单")
		}
		req.AllowFrom = []string{userID}
	}
	return SaveChannel(req)
}
