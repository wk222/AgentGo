package bridge

import (
	"context"
	"fmt"
	"log"
	"strings"

	"agentgo/internal/config"
	"agentgo/internal/governance"
	"agentgo/internal/officedoc"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
)

// officedocEnv is what the plugin needs from the app.
type officedocEnv struct {
	WorkspaceRoot func() string // current workspace, read on every call
}

func (s *AppService) officedocEnv() officedocEnv {
	if s.rt == nil {
		return officedocEnv{}
	}
	return officedocEnv{WorkspaceRoot: s.rt.WorkspaceRoot}
}

// officedocPlugin contributes native Word / Excel generation (create_docx,
// create_xlsx). Everything runs in-process in Go: no Office install and no
// sidecar. Files are written inside the current workspace only; the tools are
// always visible to the model, mutate files (so they are held back in the
// read-only canvas and go through approval like other file writes) and are
// MEDIUM risk.
//
//	AGENTGO_OFFICEDOC=0  disable the plugin
func officedocPlugin(env officedocEnv) (plugin.Plugin, bool) {
	if env.WorkspaceRoot == nil || !config.OfficeDoc.On() {
		return plugin.Plugin{}, false
	}
	return plugin.Plugin{
		Name:   "officedoc",
		Inject: []string{svcTools},
		Apply: func(c *plugin.Context) error {
			reg, err := plugin.Use[*tools.Registry](c, svcTools)
			if err != nil {
				return err
			}
			// Resolved per call: a workspace switch needs no rebinding.
			resolve := func(rel string) (string, error) {
				root := strings.TrimSpace(env.WorkspaceRoot())
				if root == "" {
					return "", fmt.Errorf("no workspace selected")
				}
				return tools.ResolveWorkspaceFile(root, rel)
			}
			ts, err := officedoc.Tools(officedoc.Env{Resolve: resolve})
			if err != nil {
				return err
			}
			for _, t := range ts {
				info, err := t.Info(context.Background())
				if err != nil {
					return err
				}
				name := info.Name
				c.Effect("declare:"+name, tools.Declare(tools.Spec{
					Name: name, Source: officedoc.Source, Static: true, Mutating: true, Risk: governance.RiskMedium,
				}))
				reg.AddTool(t)
				c.Contribute("tool", name, func() { reg.RemoveTool(name) })
			}
			log.Printf("[officedoc] %d tools", len(ts))
			return nil
		},
	}, true
}
