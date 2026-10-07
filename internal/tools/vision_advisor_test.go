package tools

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVisionAdvisorTool(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")

	// Generate a tiny dummy 100x100 png
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	assert.NoError(t, png.Encode(&buf, img))
	assert.NoError(t, os.WriteFile(imgPath, buf.Bytes(), 0644))

	r := NewRegistry()
	assert.NoError(t, RegisterVisionAdvisorTool(r, tmpDir))

	tool, ok := r.Get("vision_advisor")
	assert.True(t, ok)
	assert.NotNil(t, tool)

	res, err := r.InvokeJSON(context.Background(), "vision_advisor", `{"file_path":"test.png","prompt":"定位按钮","scenario":"desktop_ui"}`)
	assert.NoError(t, err)
	assert.Contains(t, res, "视觉顾问分析报告")
	assert.Contains(t, res, "100x100")
	assert.Contains(t, res, "desktop_ui")
}
