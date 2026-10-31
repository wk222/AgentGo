package bridge

import (
	"context"
	"database/sql"
	"log"

	"agentgo/internal/config"
	"agentgo/internal/docindex"
	"agentgo/internal/governance"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
)

// docindexEnv is what the plugin needs from the app.
type docindexEnv struct {
	DB            func() *sql.DB // the application database, which may open after the plugin
	WorkspaceRoot func() string  // current workspace, read on every call
	// FlagSource marks the memories learned from a file that has changed or been
	// deleted; nil when memory is not available.
	FlagSource tools.SourceChangeFunc
}

func (s *AppService) docindexEnv() docindexEnv {
	if s.rt == nil {
		return docindexEnv{}
	}
	return docindexEnv{
		DB: func() *sql.DB {
			if s.rt.memStore == nil {
				return nil
			}
			return s.rt.memStore.DB()
		},
		WorkspaceRoot: s.rt.WorkspaceRoot,
		FlagSource: func(ctx context.Context, path, state string) (int, error) {
			if s.rt.memStore == nil {
				return 0, nil
			}
			return s.rt.memStore.FlagDocumentSource(ctx, path, state)
		},
	}
}

// docindexPlugin contributes index_documents, list_indexed_documents and
// search_documents: a persistent keyword index over the documents the user or
// the agent chooses to index (PDF, DOCX, XLSX, text). A hit comes back with a
// citation in parse_file's terms, so it shows its page, or sheet and row, and
// opens in the source panel. The index is derived data kept in the application
// database, one per workspace; a file that changed or vanished is left out of
// answers until index_documents refreshes it. When a refresh finds that a file
// changed or was deleted, the memories learn_document built from that file are
// marked as coming from a changed source (annotation only, nothing is archived).
//
// index_documents changes the index, so it goes through approval and is held
// back in the read-only canvas and is rated MEDIUM like every mutating tool,
// although it reads only inside the workspace and writes nothing to it.
// Listing and searching change nothing and are LOW risk.
//
//	AGENTGO_DOC_INDEX=0  disable the plugin
func docindexPlugin(env docindexEnv) (plugin.Plugin, bool) {
	if env.DB == nil || env.WorkspaceRoot == nil || !config.DocIndex.On() {
		return plugin.Plugin{}, false
	}
	return plugin.Plugin{
		Name:   "docindex",
		Inject: []string{svcTools},
		Apply: func(c *plugin.Context) error {
			reg, err := plugin.Use[*tools.Registry](c, svcTools)
			if err != nil {
				return err
			}
			ts, err := tools.DocumentIndexTools(reg, docindex.New(env.DB), env.WorkspaceRoot, env.FlagSource)
			if err != nil {
				return err
			}
			for _, t := range ts {
				info, err := t.Info(context.Background())
				if err != nil {
					return err
				}
				name := info.Name
				spec := tools.Spec{Name: name, Source: tools.DocIndexSource, Static: true, Mutating: !tools.DocIndexReadOnly(name), Risk: governance.RiskLow}
				c.Effect("declare:"+name, tools.Declare(spec))
				reg.AddTool(t)
				c.Contribute("tool", name, func() { reg.RemoveTool(name) })
			}
			log.Printf("[docindex] %d tools", len(ts))
			return nil
		},
	}, true
}
