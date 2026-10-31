package bridge

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// maxImportedFileBytes bounds one dropped file. The browser build sends it as
// base64 in one JSON request (64 MiB limit), so 40 MiB of file stays under it.
const maxImportedFileBytes = 40 << 20

// ImportChatFile keeps a file the user dropped (or pasted) into the chat.
//
// A browser never tells a page where a dropped file lives on disk, only its name
// and bytes, so the chat used to attach just the name and the agent then looked
// for it inside the workspace and failed. The bytes go into <workspace>/uploads,
// and the reply carries the workspace-relative path the agent can open at once.
// The user dropped the file on purpose, so no approval is needed for this copy;
// edits land in the copy, never in the original.
func (s *AppService) ImportChatFile(name, base64Data string) map[string]any {
	root := strings.TrimSpace(s.rt.WorkspaceRoot())
	if root == "" {
		return map[string]any{"success": false, "error": "请先选择工作区文件夹，再拖入文件"}
	}
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return map[string]any{"success": false, "error": "文件内容无法解码: " + err.Error()}
	}
	if len(data) > maxImportedFileBytes {
		return map[string]any{"success": false, "error": fmt.Sprintf("文件超过 %d MB，请直接放进工作区文件夹，再用 @ 引用", maxImportedFileBytes>>20)}
	}
	rel, reused, err := storeImportedFile(root, name, data)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "path": rel, "name": filepath.Base(rel), "bytes": len(data), "reused": reused}
}

// storeImportedFile writes data to <root>/uploads under a safe version of name
// and returns the slash-separated relative path. A file that is already there
// with the same bytes is reused; a different one gets "name (2).ext" and so on.
func storeImportedFile(root, name string, data []byte) (rel string, reused bool, err error) {
	dir := filepath.Join(root, "uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, fmt.Errorf("无法创建 uploads 文件夹: %w", err)
	}
	base := safeUploadName(name)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; i <= 200; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		target := filepath.Join(dir, candidate)
		existing, rerr := os.ReadFile(target)
		switch {
		case rerr == nil && bytes.Equal(existing, data):
			return "uploads/" + candidate, true, nil
		case rerr == nil:
			continue // same name, other content: try the next number
		case !os.IsNotExist(rerr):
			return "", false, fmt.Errorf("无法读取 %s: %w", candidate, rerr)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return "", false, fmt.Errorf("无法保存文件: %w", err)
		}
		return "uploads/" + candidate, false, nil
	}
	return "", false, fmt.Errorf("uploads 里已有太多同名文件: %s", base)
}

// safeUploadName reduces whatever the browser reports to one plain file name:
// no folders, no characters Windows refuses, no reserved device names.
func safeUploadName(name string) string {
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20, strings.ContainsRune(`<>:"|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimRight(strings.TrimSpace(b.String()), ". ")
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	for utf8.RuneCountInString(name) > 120 { // keep the extension, shorten the stem
		ext := filepath.Ext(name)
		stem := []rune(strings.TrimSuffix(name, ext))
		if len(stem) <= 1 {
			break
		}
		name = string(stem[:len(stem)-1]) + ext
	}
	stem := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "LPT1", "LPT2", "LPT3":
		return "_" + name
	}
	return name
}
