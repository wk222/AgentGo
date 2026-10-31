//go:build darwin

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sandboxExec 是 macOS Seatbelt 前端。Apple 标记为弃用但仍可用，
// 目前没有公开的替代品，仍是约束子进程的标准方式。
const sandboxExec = "/usr/bin/sandbox-exec"

// Available 报告 sandbox-exec 是否存在。
func Available() bool {
	_, err := exec.LookPath(sandboxExec)
	return err == nil
}

// Command 用生成的 SBPL profile 通过 sandbox-exec 运行 `sh -c command`。
func Command(ctx context.Context, command string, p Policy) (*exec.Cmd, error) {
	if !Available() {
		return nil, ErrUnsupported
	}
	return exec.CommandContext(ctx, sandboxExec, "-p", buildProfile(p), "/bin/sh", "-c", command), nil
}

// buildProfile 渲染 SBPL。基线是 `allow default`（否则 dyld、/dev、mach 服务都会挂，
// 大多数二进制会直接 abort），在其上：
//   - 写入变白名单：先 deny 全部，再放行 WriteRoots 与常用设备节点（后写的更具体规则优先）
//   - 读取只在 $HOME 内受限：先 deny $HOME 下全部，再放行 ReadRoots。系统路径保持可读，
//     因此 ~/.ssh、~/.aws 等家目录机密被保护（macOS 的读限制仅限 $HOME，Linux 是完整白名单）
//   - 除非 AllowNetwork，否则 deny network*
func buildProfile(p Policy) string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n")

	b.WriteString("(deny file-write*)\n")
	if subs := subpaths(p.WriteRoots); subs != "" {
		b.WriteString("(allow file-write* " + subs + ")\n")
	}
	b.WriteString(`(allow file-write* (literal "/dev/null") (literal "/dev/dtracehelper") (literal "/dev/tty") (literal "/dev/stdout") (literal "/dev/stderr"))` + "\n")

	if home := homeDir(); home != "" {
		b.WriteString(fmt.Sprintf("(deny file-read* (subpath %q))\n", home))
		if subs := subpaths(p.ReadRoots); subs != "" {
			b.WriteString("(allow file-read* " + subs + ")\n")
		}
	}

	if !p.AllowNetwork {
		b.WriteString("(deny network*)\n")
	}
	return b.String()
}

// homeDir 解析 $HOME（含符号链接，使其与内核检查的规范路径一致），失败返回 ""。
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(h); err == nil {
		return real
	}
	return h
}

// subpaths 把 roots 渲染成 (subpath "…") 序列，并解析符号链接
// （如 /tmp → /private/tmp）。解析失败的原样使用。
func subpaths(roots []string) string {
	var parts []string
	for _, r := range roots {
		resolved := r
		if real, err := filepath.EvalSymlinks(r); err == nil {
			resolved = real
		}
		parts = append(parts, fmt.Sprintf("(subpath %q)", resolved))
	}
	return strings.Join(parts, " ")
}
