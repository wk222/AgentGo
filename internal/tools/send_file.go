package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/governance"
	"agentgo/internal/im"
	"agentgo/internal/pathgrant"
)

type sendFileInput struct {
	Path string `json:"path" jsonschema:"required,description=File to send: an absolute path or one relative to the workspace"`
	Name string `json:"name,omitempty" jsonschema:"description=Optional display file name shown in the chat"`
	Note string `json:"note,omitempty" jsonschema:"description=Optional short text sent before the file"`
}

type sendFileOutput struct {
	Success bool   `json:"success"`
	Path    string `json:"path,omitempty"`
	Error   string `json:"error,omitempty"`
}

// RegisterSendFileTool registers `send_file`: when this conversation came from an IM
// channel (Telegram, Feishu, ...), the agent can deliver a file produced in the
// workspace back to that chat. Sending data out of the machine is high risk, so
// governance asks for approval (the approval itself can be answered in the chat).
func RegisterSendFileTool(r *Registry, workspaceRoot string) error {
	t, err := utils.InferTool("send_file",
		"Send a local file (report, export, screenshot) to the IM chat this conversation came from. "+
			"Only works in sessions started from an IM channel (Telegram/Feishu/DingTalk/WeCom/Weixin/Discord). "+
			"Credential folders and the application's own data folder are refused.",
		func(ctx context.Context, in sendFileInput) (sendFileOutput, error) {
			mgr := im.Default()
			if mgr == nil {
				return sendFileOutput{Error: "no IM channel is connected in this host"}, nil
			}
			path := strings.TrimSpace(in.Path)
			if path == "" {
				return sendFileOutput{Error: "path is required"}, nil
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(workspaceRoot, path)
			}
			path = filepath.Clean(path)
			if why := pathgrant.Default.Forbidden(path); why != "" {
				return sendFileOutput{Error: fmt.Sprintf("refused to send %s: %s", path, why)}, nil
			}
			sid := governance.SessionIDFromContext(ctx)
			if note := strings.TrimSpace(in.Note); note != "" {
				_ = mgr.SendTextToSession(sid, note)
			}
			if err := mgr.SendFileToSession(sid, path, strings.TrimSpace(in.Name)); err != nil {
				return sendFileOutput{Error: err.Error()}, nil
			}
			return sendFileOutput{Success: true, Path: path}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
