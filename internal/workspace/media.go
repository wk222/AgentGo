package workspace

import (
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const maxMediaBytes = 20 * 1024 * 1024

// Media is a workspace file loaded for in-UI preview.
type Media struct {
	Path    string
	Mime    string
	Size    int64
	Base64  string
	DataURL string
}

var mimeByExt = map[string]string{
	".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".ico": "image/x-icon", ".bmp": "image/bmp",
}

// ReadMedia reads a binary/media file below root as base64 (20MB limit).
func ReadMedia(root, relPath string) (Media, error) {
	clean, full, err := FullPath(root, relPath, false)
	if err != nil {
		return Media{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return Media{}, err
	}
	if info.IsDir() {
		return Media{}, errors.New("path is a directory")
	}
	if info.Size() > maxMediaBytes {
		return Media{}, errors.New("file too large for preview (20MB limit)")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return Media{}, err
	}
	mime := http.DetectContentType(b)
	if m, ok := mimeByExt[strings.ToLower(filepath.Ext(clean))]; ok {
		mime = m
	}
	b64 := base64.StdEncoding.EncodeToString(b)
	return Media{Path: clean, Mime: mime, Size: info.Size(), Base64: b64, DataURL: "data:" + mime + ";base64," + b64}, nil
}
