package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"

	"agentgo/internal/capability"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
	"agentgo/internal/visionocr"
)

// The OCR plugin contributes one dynamic tool. These tests pin what would
// otherwise fail quietly: the tool is reachable by the model, stopping the plugin
// takes it away, and a missing gateway degrades to "no tool" instead of a broken
// runtime.

// newOCRService mirrors newCodetoolsService: a bare Runtime with just the services
// initPlugins needs, so these tests do not pay for a full boot.
func newOCRService(t *testing.T) (*AppService, *tools.Registry, *capability.Bus) {
	t.Helper()
	reg := tools.NewRegistry()
	bus := capability.NewBus()
	rt := &Runtime{capBus: bus, toolReg: reg, workspace: t.TempDir(), dataDir: t.TempDir()}
	rt.llm = LLMConfig{APIBase: "http://gateway.test/v1", APIKey: "test-key", Model: "chat-model"}
	s := NewAppService(rt)
	t.Cleanup(s.Close)
	return s, reg, bus
}

func TestVisionOCRPluginMountsToolWhenGatewayIsConfigured(t *testing.T) {
	t.Setenv("AGENTGO_OCR", "1")
	s, reg, bus := newOCRService(t)

	if got := pluginState(t, s, "visionocr"); got != plugin.StateRunning {
		t.Fatalf("visionocr plugin state = %q, want running; plugins = %+v", got, s.ListPlugins())
	}
	if _, ok := reg.Get("ocr_image"); !ok {
		t.Fatal("ocr_image is not in the tool registry")
	}

	// Dynamic, not static: the tool is found through tool_search. Putting it on a
	// static allowlist would create a dead entry in any build where the plugin is
	// off (see tool_catalog_guard_test.go).
	if tools.IsStaticTool("ocr_image") {
		t.Error("ocr_image must be dynamic, not static")
	}
	if st, ok := grantStatus(bus, "tool", "visionocr", "ocr_image"); !ok || st != capability.StatusPublished {
		t.Fatalf("capability grant: %v %v", st, ok)
	}
	for _, g := range bus.List("tool") {
		if g.Name == "ocr_image" {
			if g.RiskLevel != "low" {
				t.Errorf("ocr_image risk = %q, want low", g.RiskLevel)
			}
			return
		}
	}
	t.Error("ocr_image has no capability grant")
}

func TestVisionOCRPluginIsOffByDefault(t *testing.T) {
	t.Setenv("AGENTGO_OCR", "0")
	s, reg, _ := newOCRService(t)
	if _, ok := reg.Get("ocr_image"); ok {
		t.Error("ocr_image was mounted although AGENTGO_OCR=0")
	}
	// The plugin is not registered at all when the flag is off, so it must not
	// appear in the list.
	for _, st := range s.ListPlugins() {
		if st.Name == "visionocr" {
			t.Errorf("visionocr is listed although AGENTGO_OCR=0: %+v", st)
		}
	}
}

// A gateway with no key must not take the runtime down, and must not leave a tool
// that fails on every call.
func TestVisionOCRPluginSkipsWhenNoProviderAvailable(t *testing.T) {
	t.Setenv("AGENTGO_OCR", "1")
	reg := tools.NewRegistry()
	bus := capability.NewBus()
	rt := &Runtime{capBus: bus, toolReg: reg, workspace: t.TempDir(), dataDir: t.TempDir()}
	rt.llm = LLMConfig{} // no endpoint, no key
	s := NewAppService(rt)
	t.Cleanup(s.Close)

	if _, ok := reg.Get("ocr_image"); ok {
		t.Error("ocr_image was mounted without a usable gateway")
	}
}

func TestVisionOCRPluginStopRemovesTool(t *testing.T) {
	t.Setenv("AGENTGO_OCR", "1")
	s, reg, _ := newOCRService(t)

	if _, ok := reg.Get("ocr_image"); !ok {
		t.Fatal("precondition: ocr_image should be mounted")
	}
	if err := s.plugins.Stop("visionocr"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, ok := reg.Get("ocr_image"); ok {
		t.Error("ocr_image survived the plugin stopping")
	}
}

// stubProvider lets the tool contract be tested without a gateway.
type stubProvider struct {
	text   string
	noText bool
	err    error
}

func (s *stubProvider) Name() string                   { return "stub" }
func (s *stubProvider) Available(context.Context) bool { return true }
func (s *stubProvider) Recognize(context.Context, visionocr.MediaRef, visionocr.Options) (*visionocr.Result, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.noText {
		return &visionocr.Result{Provider: "stub"}, nil
	}
	return &visionocr.Result{Text: s.text, Provider: "stub"}, nil
}

// The tool is what the model actually calls, so its contract is worth testing
// without a gateway: path resolution, a missing file, an empty path.
func TestOCRImageToolContract(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.png"), []byte("not really a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool, err := visionocr.OCRImageTool(&stubProvider{text: "合计 99 元"}, func(rel string) (string, error) {
		return filepath.Join(dir, rel), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	got := runOCRImage(t, tool, `{"path":"page.png"}`)
	if got["error"] != nil && got["error"] != "" {
		t.Fatalf("unexpected error: %v", got["error"])
	}
	if got["text"] != "合计 99 元" {
		t.Fatalf("text = %v", got["text"])
	}
	if got["found"] != true {
		t.Errorf("found = %v", got["found"])
	}

	for name, args := range map[string]string{
		"missing file": `{"path":"nope.png"}`,
		"empty path":   `{"path":""}`,
	} {
		out := runOCRImage(t, tool, args)
		if out["error"] == nil || out["error"] == "" {
			t.Errorf("%s: expected an error in the output", name)
		}
	}
}

// A no-text image is a legitimate answer: the tool reports found=false and no
// error, so the model does not retry or apologise.
func TestOCRImageToolTreatsNoTextAsAnswer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blank.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool, err := visionocr.OCRImageTool(&stubProvider{noText: true}, func(rel string) (string, error) {
		return filepath.Join(dir, rel), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := runOCRImage(t, tool, `{"path":"blank.png"}`)
	if got["error"] != nil && got["error"] != "" {
		t.Errorf("no-text must not be an error: %v", got["error"])
	}
	if got["found"] != false {
		t.Errorf("found = %v, want false", got["found"])
	}
}

// A provider failure (gateway down, bad key) surfaces as an error the model can
// read, not as an empty answer that looks like "the image has no text".
func TestOCRImageToolSurfacesProviderErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool, err := visionocr.OCRImageTool(&stubProvider{err: fmt.Errorf("gateway returned HTTP 502")}, func(rel string) (string, error) {
		return filepath.Join(dir, rel), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := runOCRImage(t, tool, `{"path":"page.png"}`)
	msg, _ := got["error"].(string)
	if !strings.Contains(msg, "502") {
		t.Errorf("the provider error should reach the model, got %v", got["error"])
	}
	if got["found"] != false {
		t.Errorf("found = %v, want false on failure", got["found"])
	}
}

func runOCRImage(t *testing.T, tool any, args string) map[string]any {
	t.Helper()
	inv, ok := tool.(einotool.InvokableTool)
	if !ok {
		t.Fatalf("tool is not invokable: %T", tool)
	}
	out, err := inv.InvokableRun(context.Background(), args)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("tool output is not JSON (%q): %v", out, err)
	}
	return m
}
