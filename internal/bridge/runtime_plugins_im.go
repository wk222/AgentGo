package bridge

import (
	"context"
	"log"

	"agentgo/internal/config"
	"agentgo/internal/im"
	"agentgo/internal/plugin"
)

// imPlugins 启动 IM 双向渠道（Telegram/飞书/钉钉/企微/微信/Discord）。
// 只有 channels.yml 里有启用的实例时才会真正连接；没有就什么都不做。
func (rt *Runtime) imPlugins() []plugin.Plugin {
	return []plugin.Plugin{
		optionalPlugin("im", nil, []string{svcTaskHub}, nil, func(c *plugin.Context) error {
			// 桌面端和 agentgo-web 共用数据目录时，只能有一个进程去长轮询同一个机器人。
			if config.DisableIM.On() {
				return nil
			}
			im.SetDataDir(rt.dataDir)
			mgr := im.NewManager(NewRuntimeGateway(rt, rt.gatewayBroker))
			n, err := mgr.Start(context.Background())
			if err != nil {
				return err
			}
			if n > 0 {
				log.Printf("[im] %d bot instance(s) running", n)
			}
			// 没有实例时也登记：设置页保存配置后要靠它重新连接。
			im.SetDefault(mgr)
			c.Effect("stop im", func() {
				im.SetDefault(nil)
				mgr.Stop()
			})
			return nil
		}),
	}
}
