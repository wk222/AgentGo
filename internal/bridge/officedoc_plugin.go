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

// officedocPlugin contributes native Office tools: create_docx, create_xlsx,
// read_xlsx, edit_xlsx, create_pptx and inspect_pptx. Everything runs in-process
// in Go: no Office install, no Python or Node and no sidecar, so it works on an
// intranet PC that cannot download anything. Files are read and written inside
// the current workspace only. The tools are always visible to the model. The
// writers (create_*, edit_xlsx) are held back in the read-only canvas, go
// through approval like other file writes and are MEDIUM risk; the readers
// (read_xlsx, inspect_pptx) change nothing, stay available in the read-only
// canvas and are LOW risk.
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
				spec := tools.Spec{Name: name, Source: officedoc.Source, Static: true, Mutating: true, Risk: governance.RiskMedium}
				if officedoc.ReadOnly(name) {
					spec.Mutating, spec.Risk = false, governance.RiskLow
				}
				c.Effect("declare:"+name, tools.Declare(spec))
				reg.AddTool(t)
				c.Contribute("tool", name, func() { reg.RemoveTool(name) })
			}
			log.Printf("[officedoc] %d tools", len(ts))
			return nil
		},
	}, true
}
