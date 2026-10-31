package bridge

import (
	"fmt"
	"log"
	"strings"
	"time"

	"agentgo/internal/config"
	"agentgo/internal/governance"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
	"agentgo/internal/visionocr"
)

// visionocrEnv is what the OCR plugin needs from the runtime.
type visionocrEnv struct {
	WorkspaceRoot func() string    // current workspace, read on every call
	Endpoint      func() LLMConfig // gateway base URL + key, read on every call
}

func (s *AppService) visionocrEnv() visionocrEnv {
	if s.rt == nil {
		return visionocrEnv{}
	}
	return visionocrEnv{
		WorkspaceRoot: s.rt.WorkspaceRoot,
		Endpoint:      s.rt.LLMConfig,
	}
}

// visionocrPlugin contributes `ocr_image`, which reads text out of a picture with
// a vision model on the model gateway.
//
// The gateway path is the default because it needs no cgo, no ONNX Runtime and no
// model files, so it also works on Win7. A local engine is a separate provider
// behind the `ocr` build tag and is not required for this plugin to run.
//
//	AGENTGO_OCR=1                       mount the tool
//	AGENTGO_OCR_PROVIDER=vlm            pick a backend (empty = first available)
//	AGENTGO_OCR_VLM_MODEL=<name>        model that reads images
//
// The tool is dynamic (found through tool_search) rather than static: it is not
// used on most turns, and keeping it out of the static allowlist means no build
// variant has a dead entry in mode_policy.go.
func visionocrPlugin(env visionocrEnv) (plugin.Plugin, bool) {
	if env.Endpoint == nil || !config.OCR.On() {
		return plugin.Plugin{}, false
	}
	return plugin.Plugin{
		Name:   "visionocr",
		Inject: []string{svcTools},
		Apply: func(c *plugin.Context) error {
			reg, err := plugin.Use[*tools.Registry](c, svcTools)
			if err != nil {
				return err
			}

			vlm := visionocr.NewVLM(visionocr.VLMConfig{
				Endpoint: func() (string, string) {
					cfg := env.Endpoint()
					return cfg.APIBase, cfg.APIKey
				},
				Model: func() string {
					for _, candidate := range []string{
						config.OCRVisionModel.Get(),
						config.VisionModel.Get(),
						env.Endpoint().Model,
					} {
						if v := strings.TrimSpace(candidate); v != "" {
							return v
						}
					}
					return ""
				},
				Timeout: time.Duration(config.OCRTimeoutSec.Get()) * time.Second,
			})
			visionocr.Register("vlm", func() visionocr.Provider { return vlm })

			ctx := scopedContext(c)
			provider, err := visionocr.Pick(ctx, config.OCRProvider.Get())
			if err != nil {
				// Not a startup failure: the gateway may be unconfigured now and
				// configured later, and the rest of the runtime is unaffected.
				log.Printf("[visionocr] no provider available, ocr_image not mounted: %v", err)
				return nil
			}

			resolve := func(rel string) (string, error) {
				root := strings.TrimSpace(env.WorkspaceRoot())
				if root == "" {
					return "", fmt.Errorf("no workspace selected")
				}
				return tools.ResolveWorkspaceFile(root, rel)
			}
			t, err := visionocr.OCRImageTool(provider, resolve)
			if err != nil {
				return err
			}
			info, err := t.Info(ctx)
			if err != nil {
				return err
			}
			name := info.Name

			// Reads an image and writes nothing, so LOW and not mutating. It does
			// leave the machine (the image goes to the gateway), which is a privacy
			// fact the tool description states rather than a risk level.
			c.Effect("declare:"+name, tools.Declare(tools.Spec{
				Name:     name,
				Source:   visionocr.Source,
				Static:   false,
				Mutating: false,
				Risk:     governance.RiskLow,
			}))
			reg.AddTool(t)
			c.Contribute("tool", name, func() { reg.RemoveTool(name) })
			log.Printf("[visionocr] provider=%s", provider.Name())
			return nil
		},
	}, true
}
