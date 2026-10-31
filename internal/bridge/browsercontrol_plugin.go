package bridge

import (
	"context"
	"log"
	"path/filepath"
	"time"

	"agentgo/internal/browsercontrol"
	"agentgo/internal/config"
	"agentgo/internal/governance"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
)

// browserEnv is what the plugin needs from the app.
type browserEnv struct {
	DataDir string // app data dir; the browser profile and screenshots live below it
}

func (s *AppService) browserEnv() browserEnv {
	if s.rt == nil {
		return browserEnv{}
	}
	return browserEnv{DataDir: s.rt.DataDir()}
}

// browsercontrolPlugin contributes browser_open, browser_snapshot, browser_click,
// browser_fill, browser_press, browser_scroll, browser_back, browser_screenshot
// and browser_close: the agent drives a real Chrome or Edge window over the
// DevTools protocol, written in Go with no driver, no Node and no download, so
// it works on an intranet PC. The window is the user's too: they sign in once
// in AgentGo's own browser profile and the agent works inside that session.
//
// Opening, clicking, typing and pressing keys act on the other side, so they are
// MEDIUM risk, need approval like file writes and are held back in the
// read-only canvas. Looking, scrolling, going back, screenshots and closing
// change nothing there and are LOW risk. Every request the browser makes is
// checked against the same private-network rule as web_fetch.
//
//	AGENTGO_BROWSER_CONTROL=0           disable the plugin
//	AGENTGO_BROWSER_PATH=<exe>          Chrome/Edge to use instead of the installed one
//	AGENTGO_BROWSER_PRIVATE_HOSTS=a,b   intranet hosts the browser may open (exact names or IPs)
//	AGENTGO_BROWSER_HEADLESS=1          no visible window
func browsercontrolPlugin(env browserEnv) (plugin.Plugin, bool) {
	if env.DataDir == "" || !config.BrowserControl.On() {
		return plugin.Plugin{}, false
	}
	exe, found := browsercontrol.FindBrowser(config.BrowserPath.Get())
	if !found {
		log.Printf("[browsercontrol] no Chrome or Edge found; browser tools are not offered")
		return plugin.Plugin{}, false
	}
	return plugin.Plugin{
		Name:   "browsercontrol",
		Inject: []string{svcTools},
		Apply: func(c *plugin.Context) error {
			reg, err := plugin.Use[*tools.Registry](c, svcTools)
			if err != nil {
				return err
			}
			ctrl := browsercontrol.New(browsercontrol.Config{
				BrowserPath: exe,
				ProfileDir:  filepath.Join(env.DataDir, "browser", "profile"),
				ShotDir:     filepath.Join(env.DataDir, "browser", "screenshots"),
				Headless:    config.BrowserHeadless.On(),
				Policy:      &browsercontrol.Policy{PrivateHosts: browsercontrol.ParsePrivateHosts(config.BrowserPrivateHosts.Get())},
			})
			c.Effect("close browser", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				_ = ctrl.Close(ctx)
			})
			ts, err := browsercontrol.Tools(ctrl)
			if err != nil {
				return err
			}
			for _, t := range ts {
				info, err := t.Info(context.Background())
				if err != nil {
					return err
				}
				name := info.Name
				spec := tools.Spec{Name: name, Source: browsercontrol.Source, Static: true, Risk: governance.RiskLow}
				if browsercontrol.Acting(name) {
					spec.Mutating, spec.Risk = true, governance.RiskMedium
				}
				c.Effect("declare:"+name, tools.Declare(spec))
				reg.AddTool(t)
				c.Contribute("tool", name, func() { reg.RemoveTool(name) })
			}
			log.Printf("[browsercontrol] %d tools, browser %s", len(ts), exe)
			return nil
		},
	}, true
}
