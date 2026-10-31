package bridge

import (
	"agentgo/internal/config"
	"agentgo/internal/im"
)

// 设置页「聊天软件」卡片：读写 <数据目录>/channels.yml，并让运行中的渠道按新配置重连。
// 密钥只告诉界面“已设置”，不会发回去。

func (s *AppService) imStatus() im.AdminStatus {
	im.SetDataDir(s.rt.dataDir)
	reason := ""
	switch {
	case config.DisableIM.On():
		reason = "聊天软件功能已关闭：环境变量 AGENTGO_DISABLE_IM=1（常见于桌面端和网页版共用同一个数据目录时，只让一个进程连机器人）"
	case im.Default() == nil:
		reason = "聊天软件插件没有运行（已被停用），保存的配置不会生效"
	}
	return im.ReadAdminStatus(reason)
}

// GetIMStatus 返回 channels.yml 里的渠道、可配置的平台和各渠道是否在运行。
func (s *AppService) GetIMStatus() im.AdminStatus { return s.imStatus() }

// SaveIMChannel 保存一个渠道并立刻重新连接。密钥留空表示保持原值；启用时配置不合格会报错且不写入。
func (s *AppService) SaveIMChannel(req im.SaveRequest) (im.AdminStatus, error) {
	im.SetDataDir(s.rt.dataDir)
	if err := im.SaveChannel(req); err != nil {
		return s.imStatus(), err
	}
	if im.Default() != nil {
		if _, err := im.ReloadDefault(); err != nil {
			return s.imStatus(), err
		}
	}
	return s.imStatus(), nil
}

// RemoveIMChannel 删掉一个渠道的配置并重新连接。
func (s *AppService) RemoveIMChannel(platform string) (im.AdminStatus, error) {
	im.SetDataDir(s.rt.dataDir)
	if err := im.RemoveChannel(platform); err != nil {
		return s.imStatus(), err
	}
	if im.Default() != nil {
		if _, err := im.ReloadDefault(); err != nil {
			return s.imStatus(), err
		}
	}
	return s.imStatus(), nil
}

// ReloadIM 重新读 channels.yml 并重连所有渠道。
func (s *AppService) ReloadIM() (im.AdminStatus, error) {
	im.SetDataDir(s.rt.dataDir)
	if _, err := im.ReloadDefault(); err != nil {
		return s.imStatus(), err
	}
	return s.imStatus(), nil
}

// StartWeixinLogin 开始微信扫码登录（后台进行），用 GetWeixinLoginState 轮询二维码和进度。
func (s *AppService) StartWeixinLogin() im.LoginState {
	im.SetDataDir(s.rt.dataDir)
	return im.StartWeixinLogin()
}

// GetWeixinLoginState 返回扫码登录的进度。
func (s *AppService) GetWeixinLoginState() im.LoginState { return im.WeixinLoginState() }

// CancelWeixinLogin 中止扫码登录。
func (s *AppService) CancelWeixinLogin() im.LoginState { return im.CancelWeixinLogin() }
