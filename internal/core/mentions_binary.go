package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agentgo/internal/pathgrant"
)

// binaryDocTools names, per extension, the tools that can actually open the file.
// Pasting the raw bytes of an .xlsx into the prompt wastes the context and leaves
// the model guessing, so a mention of such a file becomes a pointer to the tool.
var binaryDocTools = map[string]string{
	".xlsx": "`read_xlsx` / `analyze_xlsx` 读取，`edit_xlsx` 修改",
	".xlsm": "`read_xlsx` / `analyze_xlsx` 读取",
	".xls":  "旧版 .xls 格式读不了，请用户另存为 .xlsx 后再处理",
	".docx": "`parse_file` 读取",
	".doc":  "旧版 .doc 格式读不了，请用户另存为 .docx 后再处理",
	".pdf":  "`parse_file` 读取",
	".pptx": "`inspect_pptx` 读取",
	".ppt":  "旧版 .ppt 格式读不了，请用户另存为 .pptx 后再处理",
}

// binaryExts are formats that are never text even without a NUL in the first bytes.
var binaryExts = map[string]bool{
	".zip": true, ".rar": true, ".7z": true, ".gz": true, ".tar": true,
	".exe": true, ".dll": true, ".so": true, ".bin": true, ".iso": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".webp": true, ".ico": true,
	".mp3": true, ".mp4": true, ".wav": true, ".avi": true, ".mov": true,
	".sqlite": true, ".db": true,
}

// binaryFileHint returns a short note for a file that must not be pasted into the
// prompt, or "" when it is text. shown is the path as the user referred to it.
func binaryFileHint(root, full, shown string, size int64) string {
	ext := strings.ToLower(filepath.Ext(full))
	tool, isDoc := binaryDocTools[ext]
	if !isDoc && !binaryExts[ext] && !sniffBinary(full) {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "#### 📎 Attached File: `%s` (%s, %d KB)\n", shown, strings.TrimPrefix(ext, "."), (size+1023)/1024)
	if isDoc {
		fmt.Fprintf(&sb, "这是二进制文档，内容没有内嵌到对话里。用 %s，参数里直接写上面的路径。", tool)
	} else {
		sb.WriteString("这是二进制文件，内容没有内嵌到对话里，不要当文本读；需要时用对应的工具处理它。")
	}
	if outsideRoot(root, full) {
		sb.WriteString("\n该文件在工作区之外：照样用上面的完整路径调用工具即可，会弹出审批卡请用户批准，批准后本次运行就能读；不要让用户先把文件复制进工作区。")
	}
	return sb.String()
}

// sniffBinary reports whether the head of the file holds a NUL byte, which text never does.
func sniffBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}

// outsideRoot reports whether full lies outside the workspace root. With no root
// known there is nothing to be outside of. The comparison resolves symlinks and
// Windows short names: the root the runtime holds and a path the user typed may
// spell the same folder differently.
func outsideRoot(root, full string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	return !pathgrant.Under(full, root)
}
