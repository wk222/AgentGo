package im

// 空导入：每个适配器在 init 里向 channel 注册表登记自己。
import (
	"agentgo/internal/imchannel/datahome"

	_ "agentgo/internal/imchannel/adapters/dingtalk"
	_ "agentgo/internal/imchannel/adapters/discord"
	_ "agentgo/internal/imchannel/adapters/feishu"
	_ "agentgo/internal/imchannel/adapters/telegram"
	_ "agentgo/internal/imchannel/adapters/wecom"
	_ "agentgo/internal/imchannel/adapters/weixin"
)

// SetDataDir 指定 channels.yml、微信登录凭据所在的数据目录。要在 NewManager 之前调用。
func SetDataDir(dir string) { datahome.SetDir(dir) }
