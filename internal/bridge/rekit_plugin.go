package bridge

import (
	"context"
	"log"
	"path/filepath"
	"strings"

	"agentgo/internal/config"
	"agentgo/internal/plugin"
	"agentgo/internal/rekit"
	"agentgo/internal/tools"
)

// rekitEnv is what the reverse-engineering plugin needs from the runtime.
type rekitEnv struct {
	WorkspaceRoot func() string // current workspace, read on every call
}

func (s *AppService) rekitEnv() rekitEnv {
	if s.rt == nil {
		return rekitEnv{}
	}
	return rekitEnv{WorkspaceRoot: s.rt.WorkspaceRoot}
}

// rekitPlugin contributes the native reverse-engineering toolkit (re_env,
// re_triage, re_apk_inspect, re_strings, re_xor_probe, re_har_summary,
// re_har_entry, re_evidence_add, re_evidence_list, re_report, re_tool_run)
// through an in-process MCP server.
//
// In ModeReverse ("reverse" harness) and ModeAdmin ("ultimate" harness) the tools
// are part of the static allowlist; in CanvasFocused the 4 mutating tools
// (re_xor_probe, re_evidence_add, re_report, re_tool_run) are held back while the
// 7 read-only static inspection tools remain available.
//
//	AGENTGO_REKIT=0              disable the plugin
//	AGENTGO_RE_HOME=<dir>        override default <workspace>/.agentgo-re case root
//	AGENTGO_RE_ALLOW_DYNAMIC=1   unlock dynamic tools (adb, frida) in re_tool_run
func rekitPlugin(env rekitEnv) (plugin.Plugin, bool) {
	if env.WorkspaceRoot == nil || !config.ReKit.On() {
		return plugin.Plugin{}, false
	}
	return plugin.Plugin{
		Name:   "rekit",
		Inject: []string{svcTools},
		Apply: func(c *plugin.Context) error {
			reg, err := plugin.Use[*tools.Registry](c, svcTools)
			if err != nil {
				return err
			}
			homeFn := func() string {
				if v := strings.TrimSpace(config.ReHome.Get()); v != "" {
					return v
				}
				if root := strings.TrimSpace(env.WorkspaceRoot()); root != "" {
					return filepath.Join(root, ".agentgo-re")
				}
				return rekit.Home("")
			}
			ts, closeFn, err := rekit.ToolsWithHome(context.Background(), homeFn, config.ReAllowDynamic.On())
			if err != nil {
				return err
			}
			if closeFn != nil {
				c.Effect("client", closeFn)
			}
			for _, t := range ts {
				info, err := t.Info(context.Background())
				if err != nil {
					return err
				}
				name := info.Name
				c.Effect("declare:"+name, tools.Declare(tools.Spec{
					Name:     name,
					Source:   rekit.Source,
					Static:   true,
					Mutating: rekit.IsMutating(name),
					Risk:     rekit.Risk(name),
				}))
				reg.AddTool(t)
				c.Contribute("tool", name, func() { reg.RemoveTool(name) })
			}
			log.Printf("[rekit] %d tools", len(ts))
			return nil
		},
	}, true
}
