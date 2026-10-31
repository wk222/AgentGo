package visionocr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// Source is the plugin name recorded in audit and capability records.
const Source = "visionocr"

type OCRImageInput struct {
	Path     string `json:"path" jsonschema:"required,description=Workspace-relative or absolute path to the image (png, jpg, gif, webp, bmp)"`
	Prompt   string `json:"prompt,omitempty" jsonschema:"description=Optional instruction for the reader, e.g. 'only the table' or 'just the serial number'. Empty reads all text."`
	LangHint string `json:"lang_hint,omitempty" jsonschema:"description=Optional language hint such as zh or en; improves accuracy on mixed documents"`
}

type OCRImageOutput struct {
	Text     string `json:"text"`
	Found    bool   `json:"found"`
	Provider string `json:"provider,omitempty"`
	Chars    int    `json:"chars,omitempty"`
	Error    string `json:"error,omitempty"`
}

// OCRImageTool builds the `ocr_image` tool over the chosen provider.
//
// resolve maps the model-supplied path to a real file inside the workspace and
// is supplied by the assembly (tools.ResolveWorkspaceFile), so this package does
// not need to know the workspace rules or the path grants.
func OCRImageTool(p Provider, resolve func(rel string) (string, error)) (tool.BaseTool, error) {
	if p == nil {
		return nil, fmt.Errorf("visionocr: provider is required")
	}
	if resolve == nil {
		return nil, fmt.Errorf("visionocr: path resolver is required")
	}
	name := "ocr_image"
	return utils.InferTool(name,
		"Read the text out of an image (screenshot, scan, photo, chart). "+
			"Returns the text in reading order, with tables kept as Markdown. "+
			"Use this instead of guessing when the user attaches a picture or asks about one in the workspace.",
		func(ctx context.Context, in OCRImageInput) (OCRImageOutput, error) {
			rel := strings.TrimSpace(in.Path)
			if rel == "" {
				return OCRImageOutput{Error: "path is required"}, nil
			}
			abs, err := resolve(rel)
			if err != nil {
				return OCRImageOutput{Error: err.Error()}, nil
			}
			if fi, statErr := os.Stat(abs); statErr != nil {
				return OCRImageOutput{Error: fmt.Sprintf("cannot read image: %v", statErr)}, nil
			} else if fi.IsDir() {
				return OCRImageOutput{Error: filepath.Base(abs) + " is a directory"}, nil
			}

			res, err := p.Recognize(ctx, MediaRef{Path: abs}, Options{
				Prompt:   strings.TrimSpace(in.Prompt),
				LangHint: strings.TrimSpace(in.LangHint),
			})
			if err != nil {
				// "no text in this image" is an answer, not a failure: returning it
				// as an error would make the model retry or apologise.
				if IsNoText(err) {
					return OCRImageOutput{Found: false, Provider: p.Name()}, nil
				}
				return OCRImageOutput{Error: err.Error()}, nil
			}
			text := strings.TrimSpace(res.Text)
			return OCRImageOutput{
				Text:     text,
				Found:    text != "",
				Provider: res.Provider,
				Chars:    len([]rune(text)),
			}, nil
		},
	)
}
