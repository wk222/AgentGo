package sandbox

// 本文件把一条 shell 命令约束在 OS 强制的文件系统/网络边界内，
// 是 governance（只看命令字符串）之下的纵深防御。
//
// 平台实现：
//   - Linux：Landlock + seccomp，通过“自重新执行垫片”施加（os_linux.go）
//   - macOS：Seatbelt / sandbox-exec（os_darwin.go）
//   - Windows：受限令牌 + 低完整性级别 + 目录标签（os_windows*.go）。只约束“写”：
//     读不受限、网络无法禁止（低完整性管不到套接字），因此要求调用方显式接受
//     AllowNetwork，否则 Command 返回 ErrUnsupported；ReadRoots 被忽略
//   - 其他：不支持，Command 返回 ErrUnsupported（调用方应 fail-closed）
//
// 移植自 octo-agent（MIT），去掉了其 datahome 依赖。

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrUnsupported：请求了沙箱但本机无法强制执行（系统不支持/内核过旧/缺少机制）。
// 明确要求沙箱的调用方必须 fail-closed。
var ErrUnsupported = errors.New("sandbox: OS confinement is not supported on this host")

// Policy 描述一条命令的文件系统与网络约束。路径都是绝对路径，
// 一个 root 授予该路径及其下全部内容的访问权。
type Policy struct {
	// ReadRoots：命令可读（及执行）的目录。
	ReadRoots []string
	// WriteRoots：命令可写的目录（也应当可读）。
	WriteRoots []string
	// AllowNetwork 为 false 时禁止 IP 网络（AF_INET/AF_INET6），Unix 域套接字仍可用。
	AllowNetwork bool
}

// DefaultPolicy 构造某个工作目录的标准约束：可读系统根目录 + cwd + 临时目录，
// 可写 cwd 与临时目录，默认无网络。$HOME 下的凭据目录（~/.ssh、~/.aws …）
// 故意不放进去，因此密钥保持不可读。extraRead 用于补充工具链目录（如 ~/go）。
func DefaultPolicy(cwd string, extraRead []string, allowNetwork bool) Policy {
	tmp := os.TempDir()

	abs := func(p string) string {
		if p == "" {
			return ""
		}
		a, err := filepath.Abs(p)
		if err != nil {
			return ""
		}
		return a
	}

	var read []string
	add := func(p string) {
		if a := abs(p); a != "" {
			read = append(read, a)
		}
	}
	add(cwd)
	add(tmp)
	for _, p := range systemReadRoots() {
		add(p)
	}
	for _, p := range extraRead {
		add(p)
	}

	var write []string
	if a := abs(cwd); a != "" {
		write = append(write, a)
	}
	if a := abs(tmp); a != "" {
		write = append(write, a)
	}
	return Policy{ReadRoots: dedupe(read), WriteRoots: dedupe(write), AllowNetwork: allowNetwork}
}

// SplitRoots 把 PATH 风格的列表（os.PathListSeparator 分隔）拆成目录列表，忽略空项。
func SplitRoots(list string) []string {
	var out []string
	for _, p := range strings.Split(list, string(os.PathListSeparator)) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// systemReadRoots：普通命令需要的系统只读目录（工具链、共享库、配置）。
// 只放目录；/dev/null 之类的设备文件由平台层单独授予。路径不存在无害。
func systemReadRoots() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/usr", "/bin", "/sbin", "/etc", "/var", "/private", "/System", "/Library", "/opt"}
	case "linux":
		return []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt", "/proc"}
	default:
		return nil
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func containsRoot(roots []string, target string) bool {
	for _, r := range roots {
		if r == target {
			return true
		}
	}
	return false
}
