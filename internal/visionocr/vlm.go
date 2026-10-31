package visionocr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VLMConfig is what the vision provider needs from the application. It is
// injected at assembly time rather than read here, because the gateway address
// and key live in config.json behind bridge.ModelSource, and this package must
// stay usable (and testable) without an application.
type VLMConfig struct {
	// Endpoint returns the OpenAI-compatible base URL and key on every call, so a
	// settings change does not require a restart.
	Endpoint func() (apiBase, apiKey string)
	// Model returns the vision model name; empty falls back to the chat model the
	// same Endpoint describes.
	Model func() string
	// Timeout bounds one recognition call. Zero uses a sane default.
	Timeout time.Duration
}

// VLMProvider reads text through a vision model on the model gateway.
//
// This is the default path on purpose: an intranet deployment that already has a
// gateway with a vision model needs no extra runtime, no cgo and no model files,
// and it works on Windows 7. A local ONNX engine is the exception, not the rule.
type VLMProvider struct {
	cfg VLMConfig
}

// NewVLM returns a provider bound to cfg. It reports unavailable until both an
// endpoint and a key are supplied.
func NewVLM(cfg VLMConfig) *VLMProvider { return &VLMProvider{cfg: cfg} }

func (p *VLMProvider) Name() string { return "vlm" }

// Available reports whether a gateway and key are configured. It does not call
// the model: a network probe during boot would make startup depend on the
// gateway being up.
func (p *VLMProvider) Available(context.Context) bool {
	if p == nil || p.cfg.Endpoint == nil {
		return false
	}
	base, key := p.cfg.Endpoint()
	return strings.TrimSpace(base) != "" && strings.TrimSpace(key) != ""
}

// defaultOCRPrompt asks for exactly what the caller needs and nothing else.
// Left to its own devices a vision model narrates the image ("a screenshot of a
// settings dialog..."), which is noise in an OCR result.
const defaultOCRPrompt = `Read all visible text in this image and output it as plain text.

Rules:
- Preserve the reading order and line breaks.
- For tables, output a Markdown table so rows stay together.
- Reproduce the text exactly, including numbers and punctuation. Do not translate.
- Do not describe the image, do not summarize, do not add commentary.
- If the image contains no text at all, reply with exactly: no text`

// ocrRequest is the OpenAI-compatible chat payload with an image part. Eino's
// schema types are not used here because this must stay a plain HTTP call: the
// provider runs even when no agent turn is in progress.
type ocrRequest struct {
	Model       string       `json:"model"`
	Messages    []ocrMessage `json:"messages"`
	Temperature float64      `json:"temperature"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
}

type ocrMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type ocrContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *ocrImagePart `json:"image_url,omitempty"`
}

type ocrImagePart struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

func (p *VLMProvider) Recognize(ctx context.Context, img MediaRef, opts Options) (*Result, error) {
	if !p.Available(ctx) {
		return nil, fmt.Errorf("visionocr: vlm provider has no endpoint or key configured")
	}
	apiBase, apiKey := p.cfg.Endpoint()
	apiBase = strings.TrimRight(strings.TrimSpace(apiBase), "/")
	model := ""
	if p.cfg.Model != nil {
		model = strings.TrimSpace(p.cfg.Model())
	}
	if model == "" {
		return nil, fmt.Errorf("visionocr: no vision model configured")
	}

	dataURL, err := imageDataURL(img, opts.MaxPixels)
	if err != nil {
		return nil, err
	}

	prompt := strings.TrimSpace(opts.Prompt)
	if prompt == "" {
		prompt = defaultOCRPrompt
	}
	if opts.LangHint != "" {
		prompt += "\nThe text is likely in language: " + opts.LangHint + "."
	}

	body, err := json.Marshal(ocrRequest{
		Model: model,
		Messages: []ocrMessage{{
			Role: "user",
			Content: []ocrContentPart{
				{Type: "text", Text: prompt},
				{Type: "image_url", ImageURL: &ocrImagePart{URL: dataURL}},
			},
		}},
		Temperature: 0,
	})
	if err != nil {
		return nil, err
	}

	timeout := p.cfg.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, apiBase+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("visionocr: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("visionocr: gateway returned HTTP %d: %s", resp.StatusCode, firstLine(string(raw)))
	}

	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("visionocr: decode gateway response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return nil, fmt.Errorf("visionocr: gateway returned no choices")
	}

	text, err := Validate(decoded.Choices[0].Message.Content)
	if err != nil {
		return nil, err
	}
	return &Result{Text: text, Provider: p.Name()}, nil
}

// imageDataURL loads the image and inlines it as a data: URI. The gateway accepts
// remote URLs too, but a workspace file is not reachable from the gateway's point
// of view, so inlining keeps a single code path.
func imageDataURL(img MediaRef, maxPixels int) (string, error) {
	if strings.HasPrefix(strings.TrimSpace(img.DataURL), "data:") {
		return img.DataURL, nil
	}
	path := strings.TrimSpace(img.Path)
	if path == "" {
		return "", fmt.Errorf("visionocr: image reference has no path or data URL")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("visionocr: read %s: %w", path, err)
	}
	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType == "" {
		if _, format, derr := image.DecodeConfig(bytes.NewReader(raw)); derr == nil {
			mimeType = "image/" + format
		}
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
	_ = maxPixels // downscaling needs a decoder round-trip; the gateway resizes anyway
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
