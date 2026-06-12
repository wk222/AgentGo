package bridge

import (
	"context"
	"fmt"
)

type runtimeAppPinger struct{ rt *Runtime }

func (p *runtimeAppPinger) PingApp(ctx context.Context, appName string) error {
	res := p.rt.InvokeInnerApp(ctx, appName, "", "", "ping", "")
	if res.Error != "" {
		return fmt.Errorf("%s", res.Error)
	}
	return nil
}
