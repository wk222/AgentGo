package visionocr

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeProvider is a Provider with a settable availability, so the selection
// logic can be tested without a gateway or a model.
type fakeProvider struct {
	name   string
	avail  bool
	result *Result
	err    error
}

func (f *fakeProvider) Name() string                   { return f.name }
func (f *fakeProvider) Available(context.Context) bool { return f.avail }
func (f *fakeProvider) Recognize(context.Context, MediaRef, Options) (*Result, error) {
	return f.result, f.err
}

func resetRegistry(t *testing.T) {
	t.Helper()
	regMu.Lock()
	savedReg, savedDefault := registry, defaultID
	registry = map[string]func() Provider{}
	defaultID = ""
	regMu.Unlock()
	t.Cleanup(func() {
		regMu.Lock()
		registry, defaultID = savedReg, savedDefault
		regMu.Unlock()
	})
}

func TestPickPrefersAvailableProviders(t *testing.T) {
	resetRegistry(t)
	Register("aaa-missing", func() Provider { return &fakeProvider{name: "aaa-missing", avail: false} })
	Register("zzz-ready", func() Provider { return &fakeProvider{name: "zzz-ready", avail: true} })

	// Sorted order would pick aaa-missing first; availability must win.
	p, err := Pick(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "zzz-ready" {
		t.Fatalf("picked %q, want the available provider", p.Name())
	}
}

func TestPickHonoursExplicitChoice(t *testing.T) {
	resetRegistry(t)
	Register("a", func() Provider { return &fakeProvider{name: "a", avail: true} })
	Register("b", func() Provider { return &fakeProvider{name: "b", avail: true} })

	p, err := Pick(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "b" {
		t.Fatalf("picked %q, want b", p.Name())
	}

	// An explicit choice that is unavailable is an error, not a silent fallback:
	// the operator asked for it by name and should hear that it cannot run.
	if _, err := Pick(context.Background(), "missing"); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
}

func TestPickReportsWhenNothingIsAvailable(t *testing.T) {
	resetRegistry(t)
	Register("only", func() Provider { return &fakeProvider{name: "only", avail: false} })
	if _, err := Pick(context.Background(), ""); err == nil {
		t.Fatal("expected an error when no provider is available")
	} else if !strings.Contains(err.Error(), "only") {
		t.Errorf("the error should name the registered providers: %v", err)
	}
}

// The VLM provider is the path that works on Win7, so its request shape is worth
// pinning: an OpenAI-compatible gateway expects the image as a data URL inside a
// content array, and a change to that shape fails at runtime, not at build time.
func TestVLMRecognizeSpeaksOpenAICompatibleProtocol(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "shot.png")
	writeTestPNG(t, imgPath)

	var gotModel string
	var gotImageURL string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("wrong path %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content []struct {
					Type     string `json:"type"`
					Text     string `json:"text"`
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotModel = body.Model
		for _, part := range body.Messages[0].Content {
			if part.Type == "image_url" {
				gotImageURL = part.ImageURL.URL
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": "第一行文字\n第二行文字"},
			}},
		})
	}))
	defer srv.Close()

	p := NewVLM(VLMConfig{
		Endpoint: func() (string, string) { return srv.URL, "test-key" },
		Model:    func() string { return "qwen-vl-test" },
		Timeout:  5 * time.Second,
	})
	res, err := p.Recognize(context.Background(), MediaRef{Path: imgPath}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "第一行文字\n第二行文字" {
		t.Fatalf("text = %q", res.Text)
	}
	if res.Provider != "vlm" {
		t.Errorf("provider = %q", res.Provider)
	}
	if gotModel != "qwen-vl-test" {
		t.Errorf("model sent = %q", gotModel)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("authorization = %q", gotAuth)
	}
	if !strings.HasPrefix(gotImageURL, "data:image/png;base64,") {
		t.Errorf("image should be inlined as a png data URL, got %.40q", gotImageURL)
	}
}

func TestVLMReportsGatewayErrors(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "shot.png")
	writeTestPNG(t, imgPath)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	p := NewVLM(VLMConfig{
		Endpoint: func() (string, string) { return srv.URL, "bad" },
		Model:    func() string { return "m" },
		Timeout:  5 * time.Second,
	})
	_, err := p.Recognize(context.Background(), MediaRef{Path: imgPath}, Options{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("the status code should survive: %v", err)
	}
	// The key must never reach a log line or an error message.
	if strings.Contains(err.Error(), "bad") {
		t.Errorf("error leaked the key: %v", err)
	}
}

func TestVLMUnavailableWithoutEndpointOrKey(t *testing.T) {
	cases := []struct {
		name string
		base string
		key  string
	}{
		{"neither", "", ""},
		{"no key", "http://gw", ""},
		{"no base", "", "k"},
	}
	for _, tc := range cases {
		p := NewVLM(VLMConfig{Endpoint: func() (string, string) { return tc.base, tc.key }})
		if p.Available(context.Background()) {
			t.Errorf("%s: should be unavailable", tc.name)
		}
	}
	var nilProvider *VLMProvider
	if nilProvider.Available(context.Background()) {
		t.Error("a nil provider must report unavailable, not panic")
	}
}

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
