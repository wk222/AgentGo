package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/artifact"
	"agentgo/internal/pathgrant"
)

type deliverFileInput struct {
	Path  string `json:"path" jsonschema:"description=The finished file to hand to the user\\, absolute or relative to the workspace"`
	Title string `json:"title,omitempty" jsonschema:"description=A short name for it in the conversation\\, e.g. 'Q3 sales report'"`
}

type deliverFileOutput struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	artifact.Record
}

// RegisterDeliverFileTool registers `deliver_file`: the way to hand a finished file to the user. The file is
// checked (it must exist), copied so the user keeps the delivered version, recorded in the conversation and
// offered as a download. Saying "I saved it to ..." in prose is not a delivery.
func RegisterDeliverFileTool(r *Registry, workspaceRoot string) error {
	t, err := utils.InferTool("deliver_file",
		"Hand a finished file (report, export, spreadsheet, image ...) to the user. It checks that the file really exists, keeps a copy of "+
			"this version for download, and records it in the conversation. Delivering the same content again adds nothing; "+
			"changed content becomes the next version. Do this at the end of a task that produced a file, instead of only describing the path. "+
			"A file the user removed from the deliverables is not attached again unless its content changed.",
		func(ctx context.Context, in deliverFileInput) (deliverFileOutput, error) {
			path := strings.TrimSpace(in.Path)
			if path == "" {
				return deliverFileOutput{Error: "path is required"}, nil
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(workspaceRoot, path)
			}
			path = filepath.Clean(path)
			if why := pathgrant.Default.Forbidden(path); why != "" {
				return deliverFileOutput{Error: fmt.Sprintf("refused to deliver %s: %s", path, why)}, nil
			}
			rec, err := artifact.Deliver(ctx, path, in.Title)
			if err != nil {
				return deliverFileOutput{Error: err.Error()}, nil
			}
			return deliverFileOutput{Success: true, Record: rec}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
