// Package visionocr reads text out of images.
//
// It is a provider registry rather than one engine, because the three realistic
// ways to do OCR here differ in what they can depend on:
//
//   - vlm    — a vision model on the model gateway. No cgo, no models on disk,
//     works on Win7, and returns layout hints a pure text recognizer cannot.
//   - remote — an HTTP OCR service (another machine, or agentgo-ocr.exe).
//   - onnx   — a local PP-OCRv6 engine. Needs cgo and an ONNX Runtime build that
//     Windows 7 cannot load (its DLL statically imports CreateFile2).
//
// Only the last one is behind the `ocr` build tag. The package and the other
// providers are always compiled, so the Win7 build keeps OCR as long as the
// gateway has a vision model.
//
// The output of every provider goes through Validate before it reaches a model
// or an index: OCR text is untrusted input, and a vision model in particular can
// emit a plausible prefix followed by a decoding loop.
package visionocr

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Line is one recognized text line, with its position when the provider knows
// it. Coordinates are pixels in the source image, origin at the top left.
type Line struct {
	Text       string  `json:"text"`
	X          int     `json:"x"`
	Y          int     `json:"y"`
	W          int     `json:"w"`
	H          int     `json:"h"`
	Confidence float64 `json:"confidence,omitempty"`
}

// Result is what a provider returns. Text is the newline-joined reading order,
// so a caller that does not care about layout never has to touch Lines.
type Result struct {
	Text       string  `json:"text"`
	Lines      []Line  `json:"lines,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Provider   string  `json:"provider"`
	Pages      int     `json:"pages,omitempty"`
}

// Options tunes one recognition call.
type Options struct {
	// Prompt overrides the provider's default instruction (VLM providers only).
	Prompt string
	// LangHint is a BCP-47-ish tag such as "zh" or "en". Empty means auto.
	LangHint string
	// Layout asks the provider to keep table structure when it can.
	Layout bool
	// MaxPixels downscales the image before sending. Zero uses the provider default.
	MaxPixels int
}

// MediaRef points at an image without inlining its bytes.
type MediaRef struct {
	// Path is a workspace-relative or absolute file path.
	Path string
	// DataURL is a data: URI, used when the image was pasted rather than saved.
	DataURL string
}

// Provider is one way of reading text out of an image.
type Provider interface {
	// Name is the identifier used in configuration and in tool output.
	Name() string
	// Available reports whether this provider could run right now. It must be
	// cheap: it is called during runtime assembly to pick a default.
	Available(ctx context.Context) bool
	Recognize(ctx context.Context, img MediaRef, opts Options) (*Result, error)
}

var (
	regMu     sync.RWMutex
	registry  = map[string]func() Provider{}
	defaultID string
)

// Register makes a provider constructor available under name. Constructors are
// called lazily, so a provider that needs configuration can be registered from
// an init or a plugin and still read its settings at use time.
func Register(name string, ctor func() Provider) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || ctor == nil {
		return
	}
	regMu.Lock()
	registry[name] = ctor
	regMu.Unlock()
}

// SetDefault names the provider to use when the caller does not choose one.
func SetDefault(name string) {
	regMu.Lock()
	defaultID = strings.ToLower(strings.TrimSpace(name))
	regMu.Unlock()
}

// Get returns the named provider, or the default when name is empty. A provider
// that is registered but whose configuration is missing still comes back; the
// caller checks Available.
func Get(name string) (Provider, error) {
	regMu.RLock()
	ctor, ok := registry[strings.ToLower(strings.TrimSpace(name))]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("visionocr: unknown provider %q", name)
	}
	p := ctor()
	if p == nil {
		return nil, fmt.Errorf("visionocr: provider %q returned nil", name)
	}
	return p, nil
}

// Names lists the registered providers in sorted order.
func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Pick returns the first available provider: the named one when given, then the
// configured default, then the others in sorted order. This is what keeps a
// single binary usable across an intranet desk with a vision gateway, a machine
// with a local engine, and a box with neither.
func Pick(ctx context.Context, prefer string) (Provider, error) {
	if strings.TrimSpace(prefer) != "" {
		p, err := Get(prefer)
		if err != nil {
			return nil, err
		}
		if !p.Available(ctx) {
			return nil, fmt.Errorf("visionocr: provider %q is not available", prefer)
		}
		return p, nil
	}
	regMu.RLock()
	def := defaultID
	regMu.RUnlock()
	var order []string
	if def != "" {
		order = append(order, def)
	}
	for _, n := range Names() {
		if n != def {
			order = append(order, n)
		}
	}
	for _, n := range order {
		p, err := Get(n)
		if err == nil && p.Available(ctx) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("visionocr: no provider is available (registered: %s)", strings.Join(Names(), ", "))
}
