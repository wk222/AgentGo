package agentpack

import (
	"agentgo/internal/tools"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestImportToolFailurePreservesPreviousDefinition(t *testing.T) {
	for _, scenario := range []string{"invalid-definition", "registration-failure", "without-registry"} {
		t.Run(scenario, func(t *testing.T) {
			newStore := func() *tools.DynamicStore {
				db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "tools.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				store, err := tools.NewDynamicStore(db)
				if err != nil {
					t.Fatal(err)
				}
				return store
			}
			store := newStore()
			old := tools.DynamicToolDef{Name: "preserve_tool", Description: "working version", Parameters: "{}", EnvVars: []string{"APPROVED_KEY"}}
			if err := store.Save(old); err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry()
			reg.SetDynamicSandbox(t.TempDir())
			if err := reg.RegisterDynamicTool(old); err != nil {
				t.Fatal(err)
			}
			incoming := tools.DynamicToolDef{Name: old.Name, Description: "new version", Parameters: "not-json"}
			if scenario == "registration-failure" {
				incoming.Parameters = "{}"
				reg.SetDynamicSandbox("")
			}
			source := newStore()
			if err := source.Save(incoming); err != nil {
				t.Fatal(err)
			}
			exporter := &Engine{Dyn: source, OutDir: t.TempDir()}
			ctx := context.Background()
			path, _, err := exporter.Export(ctx, ExportRequest{Tools: []string{old.Name}})
			if err != nil {
				t.Fatal(err)
			}
			e := &Engine{Dyn: store, Reg: reg}
			if scenario == "without-registry" {
				e.Reg = nil
			}
			res, err := e.Import(ctx, path, true, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Installed) != 0 || len(res.Skipped) != 1 {
				t.Fatalf("bad import not rejected: %+v", res)
			}
			got, err := store.Get(old.Name)
			if err != nil {
				t.Fatalf("previous tool lost: %v", err)
			}
			if got.Description != old.Description || got.Parameters != old.Parameters || len(got.EnvVars) != 1 || got.EnvVars[0] != "APPROVED_KEY" {
				t.Fatalf("previous definition changed: %+v", got)
			}
			if _, ok := reg.Get(old.Name); !ok {
				t.Fatal("live previous tool lost")
			}
		})
	}
}
