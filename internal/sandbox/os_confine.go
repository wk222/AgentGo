package sandbox

import (
	"context"
	"os/exec"

	"agentgo/internal/shellcmd"
)

// Confine 返回一条在 OS 沙箱内运行 line 的命令（cwd=dir，UTF-8 环境）。
// 调用方照常设置 Stdout/Stderr 并用 shellcmd.Run 启动，因此超时与进程树清理行为不变。
// 本机不支持 OS 沙箱时返回 ErrUnsupported，要求沙箱的调用方必须 fail-closed。
func Confine(ctx context.Context, line, dir string, extraRead []string, allowNetwork bool) (*exec.Cmd, error) {
	cmd, err := Command(ctx, line, DefaultPolicy(dir, extraRead, allowNetwork))
	if err != nil {
		return nil, err
	}
	cmd.Dir = dir
	cmd.Env = shellcmd.WithUTF8Env(cmd.Env)
	return cmd, nil
}
