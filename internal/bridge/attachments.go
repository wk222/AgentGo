package bridge

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// UploadAttachment saves a base64-encoded image pasted from the frontend
// into the local data/attachments directory, returning its web path.
func (s *AppService) UploadAttachment(base64Data, mimeType string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64: %w", err)
	}

	ext := ".jpg"
	mime := strings.ToLower(mimeType)
	switch {
	case strings.Contains(mime, "png"):
		ext = ".png"
	case strings.Contains(mime, "gif"):
		ext = ".gif"
	case strings.Contains(mime, "webp"):
		ext = ".webp"
	}

	// Ensure attachments directory exists
	dir := filepath.Join(s.rt.DataDir(), "attachments")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create attachments directory: %w", err)
	}

	filename := fmt.Sprintf("attach_%s%s", uuid.New().String()[:12], ext)
	targetPath := filepath.Join(dir, filename)

	if err := os.WriteFile(targetPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write attachment file: %w", err)
	}

	// Return the web path intercepted by our custom asset server
	return "/attachments/" + filename, nil
}
