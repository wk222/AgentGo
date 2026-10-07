package bridge

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	ct "github.com/charmbracelet/crush/pkg/codetools"

	"agentgo/internal/codetools"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
)

// codetoolsEnv is everything the plugin needs from the app, spelled out so the
// plugin does not hold the whole AppService.
type codetoolsEnv struct {
	WorkspaceRoot func() string     // current workspace; read each time the plugin starts
	DataDir       string            // app data dir; the toolbox keeps its state below it
	Publish       func(*ct.Toolbox) // hands the live toolbox to the app (LSP events, diagnostics); nil on stop
}

// codetoolsEnv wires the plugin to this app. It is the only place the plugin's
// dependencies touch AppService/Runtime; a nil Runtime disables the plugin.
func (s *AppService) codetoolsEnv() codetoolsEnv {
	if s.rt == nil {
		return codetoolsEnv{}
	}
	return codetoolsEnv{
		WorkspaceRoot: s.rt.WorkspaceRoot,
		DataDir:       s.rt.DataDir(),
		Publish:       s.setCodetools,
	}
}

// rebindCodetools is the workspace binder: it moves the code tools to the new
// workspace by reloading the plugin, and fails if they did not come back up so
// that Runtime.SetWorkspaceRoot can roll the switch back. A plugin the user
// stopped on purpose stays stopped.
func (s *AppService) rebindCodetools(string) error {
	if s.plugins == nil {
		return nil
	}
	st, ok := s.plugins.Status("codetools")
	if !ok || st.State == plugin.StateStopped {
		return nil
	}
	if err := s.plugins.Reload("codetools"); err != nil {
		return fmt.Errorf("reload codetools: %w", err)
	}
	if st, _ := s.plugins.Status("codetools"); st.State != plugin.StateRunning {
		return fmt.Errorf("codetools did not restart (%s): %s", st.State, st.Error)
	}
	return nil
}

// codetoolsPlugin contributes Crush's code-aware tools (LSP diagnostics,
// references, symbols, view/edit/write, grep/glob/ls) to AgentGo's tool
// registry, so the Eino agent can diagnose and fix code on its own.
//
//	AGENTGO_CODETOOLS=0          disable the plugin
//	AGENTGO_CODETOOLS_APPROVE=0  deny every mutating action (read-only tools)
//
// The toolbox is bound to the workspace root when the plugin starts. A
// workspace switch goes through Runtime.SetWorkspaceRoot, which runs the binder
// registered in initPlugins and reloads this plugin, so the tools, grants,
// visibility and language servers move together or the switch is rolled back.
func codetoolsPlugin(env codetoolsEnv) (plugin.Plugin, bool) {
	if env.WorkspaceRoot == nil || os.Getenv("AGENTGO_CODETOOLS") == "0" {
		return plugin.Plugin{}, false
	}
	return plugin.Plugin{
		Name:   "codetools",
		Inject: []string{svcTools},
		Apply: func(c *plugin.Context) error {
			reg, err := plugin.Use[*tools.Registry](c, svcTools)
			if err != nil {
				return err
			}
			root := strings.TrimSpace(env.WorkspaceRoot())
			if root == "" {
				return fmt.Errorf("no workspace selected")
			}
			opts := ct.Options{WorkDir: root, DataDir: filepath.Join(env.DataDir, "codetools")}
			if os.Getenv("AGENTGO_CODETOOLS_APPROVE") == "0" {
				opts.Approver = func(context.Context, ct.Request) bool { return false }
			}
			tb, err := ct.New(opts)
			if err != nil {
				return err
			}
			env.Publish(tb)
			c.Effect("close codetools", func() {
				env.Publish(nil)
				tb.Close()
			}) // registered first => released last

			adapted, err := codetools.Adapt(tb.Tools)
			if err != nil {
				return err
			}
			c.Effect("tool traits", codetools.RegisterTraits(tb.Tools))
			for i, t := range adapted {
				info, err := t.Info(context.Background())
				if err != nil {
					return err
				}
				name := info.Name
				reg.AddTool(t)
				c.Contribute("tool", name, func() { reg.RemoveTool(name) })
				if codetools.AlwaysVisible(tb.Tools[i].Name) {
					c.Effect("static:"+name, tools.RegisterStaticTool(name, codetools.IsMutating(tb.Tools[i].Name)))
				}
			}
			log.Printf("[codetools] %d tools bound to %s", len(adapted), root)
			return nil
		},
	}, true
}
