//go:build linux

package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// policyEnv 把 JSON 编码的 Policy 传给重新执行的垫片。
// 这是父子进程之间的内部通道，不是用户设置，所以不叫 AGENTGO_*（那类名字只能由 internal/config 声明）。
const policyEnv = "SANDBOX_SHIM_POLICY"

// shimArg 是父进程重新执行时使用的隐藏子命令：垫片给自己施加 Landlock + seccomp，
// 然后 exec 真正的命令。任何链接了本包的二进制都能充当垫片（见 init）。
const shimArg = "__agentgo-sandboxed-exec"

// 即使不在写根目录下，被约束的命令也可读写的设备文件。
var deviceFiles = []string{"/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom", "/dev/tty"}

func init() {
	// init 在主 goroutine 上运行，且被锁定在主线程，正好满足“施加限制与 execve 在同一 OS 线程”。
	if len(os.Args) > 1 && os.Args[1] == shimArg {
		os.Exit(ShimMain())
	}
}

// Available 报告 Landlock 是否可用（内核 >= 5.13 且已启用）。
func Available() bool { return landlockABI() >= 1 }

// landlockABI 返回内核 Landlock ABI 版本，不支持返回 -1。
func landlockABI() int {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, uintptr(unix.LANDLOCK_CREATE_RULESET_VERSION))
	if errno != 0 {
		return -1
	}
	return int(v)
}

// Command 重新执行当前二进制作为沙箱垫片：策略经环境变量传递，真正的命令放在 "--" 之后。
func Command(ctx context.Context, command string, p Policy) (*exec.Cmd, error) {
	if !Available() {
		return nil, ErrUnsupported
	}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("sandbox: locate self: %w", err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("sandbox: marshal policy: %w", err)
	}
	cmd := exec.CommandContext(ctx, self, shimArg, "--", "/bin/sh", "-c", command)
	cmd.Env = append(os.Environ(), policyEnv+"="+string(data))
	return cmd, nil
}

// ShimMain 是重新执行的入口：给自己施加沙箱，再 exec "--" 后面的命令。成功时不返回。
func ShimMain() int {
	runtime.LockOSThread()

	cmdArgs := argsAfterDoubleDash(os.Args)
	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "sandbox shim: no command after --")
		return 1
	}

	var p Policy
	if raw := os.Getenv(policyEnv); raw != "" {
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox shim: bad policy: %v\n", err)
			return 1
		}
	}

	if err := applyLandlock(p); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox shim: landlock: %v\n", err)
		return 1
	}
	if !p.AllowNetwork {
		if err := applyNoNetwork(); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox shim: seccomp: %v\n", err)
			return 1
		}
	}

	// 策略变量只给垫片用，不泄漏给被约束的命令。
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if len(e) > len(policyEnv) && e[:len(policyEnv)+1] == policyEnv+"=" {
			continue
		}
		env = append(env, e)
	}

	path := cmdArgs[0]
	if resolved, err := exec.LookPath(path); err == nil {
		path = resolved
	}
	if err := syscall.Exec(path, cmdArgs, env); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox shim: exec %s: %v\n", path, err)
		return 1
	}
	return 0 // unreachable
}

func argsAfterDoubleDash(args []string) []string {
	for i, a := range args {
		if a == "--" {
			return args[i+1:]
		}
	}
	return nil
}

// handledAccessFS 返回要管控的全部文件系统访问权，并按内核 ABI 版本裁剪
// （旧内核上多余的位会让 create_ruleset 失败）。
func handledAccessFS(abi int) uint64 {
	a := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		a |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		a |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	return a
}

// applyLandlock 把本进程限制在策略的文件系统根内：ReadRoots 可读(+执行)，
// WriteRoots 完全读写，外加少量设备文件读写。不存在的路径被跳过。
func applyLandlock(p Policy) error {
	abi := landlockABI()
	if abi < 1 {
		return ErrUnsupported
	}
	handled := handledAccessFS(abi)

	attr := struct{ accessFS uint64 }{accessFS: handled}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("create_ruleset: %w", errno)
	}
	rulesetFd := int(fd)
	defer unix.Close(rulesetFd)

	readDir := handled & uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE|
		unix.LANDLOCK_ACCESS_FS_READ_DIR|
		unix.LANDLOCK_ACCESS_FS_EXECUTE)

	for _, root := range p.WriteRoots {
		if err := addPathRule(rulesetFd, root, handled); err != nil {
			return err
		}
	}
	for _, root := range p.ReadRoots {
		if containsRoot(p.WriteRoots, root) {
			continue
		}
		if err := addPathRule(rulesetFd, root, readDir); err != nil {
			return err
		}
	}
	// 设备文件：字符设备上只有读/写文件权限有效（EXECUTE/TRUNCATE 会让 add_rule 返回 EINVAL）。
	// 尽力而为——设备规则失败绝不能让整个沙箱中止。
	devAccess := handled & uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE|unix.LANDLOCK_ACCESS_FS_WRITE_FILE)
	for _, dev := range deviceFiles {
		_ = addPathRule(rulesetFd, dev, devAccess)
	}

	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(rulesetFd), 0, 0); errno != 0 {
		return fmt.Errorf("restrict_self: %w", errno)
	}
	return nil
}

// addPathRule 授予 path 之下的访问权。路径不存在时跳过（不报错），
// 这样宽松的默认根目录无害。
func addPathRule(rulesetFd int, path string, access uint64) error {
	if access == 0 {
		return nil
	}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer unix.Close(fd)

	attr := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(fd)}
	_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFd), uintptr(unix.LANDLOCK_RULE_PATH_BENEATH),
		uintptr(unsafe.Pointer(&attr)), 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("add_rule %s: %w", path, errno)
	}
	return nil
}

// applyNoNetwork 安装 seccomp 过滤器：socket(AF_INET/AF_INET6) 返回 EACCES，
// AF_UNIX 与其他系统调用不受影响。这会阻断被约束命令的 IP 网络（含 DNS）。
//
// 局限：过滤器按原生 socket(2) 系统调用号匹配，32 位/compat 系统调用可绕过——
// 对“普通命令”的威胁模型可接受。
func applyNoNetwork() error {
	const (
		denyRet  = uint32(unix.SECCOMP_RET_ERRNO) | (uint32(unix.EACCES) & 0x0000ffff)
		allowRet = uint32(unix.SECCOMP_RET_ALLOW)
		offNR    = 0  // seccomp_data.nr
		offArg0  = 16 // seccomp_data.args[0]
	)
	filter := []unix.SockFilter{
		bpfStmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, offNR),
		bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, uint32(unix.SYS_SOCKET), 0, 3), // 不是 socket → 放行
		bpfStmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, offArg0),                        // domain
		bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, uint32(unix.AF_INET), 2, 0),    // AF_INET → 拒绝
		bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, uint32(unix.AF_INET6), 1, 0),   // AF_INET6 → 拒绝
		bpfStmt(unix.BPF_RET|unix.BPF_K, allowRet),
		bpfStmt(unix.BPF_RET|unix.BPF_K, denyRet),
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}

	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, uintptr(unix.SECCOMP_MODE_FILTER),
		uintptr(unsafe.Pointer(&prog)), 0, 0); err != nil {
		return fmt.Errorf("set_seccomp: %w", err)
	}
	return nil
}

func bpfStmt(code uint16, k uint32) unix.SockFilter {
	return unix.SockFilter{Code: code, K: k}
}

func bpfJump(code uint16, k uint32, jt, jf uint8) unix.SockFilter {
	return unix.SockFilter{Code: code, Jt: jt, Jf: jf, K: k}
}
