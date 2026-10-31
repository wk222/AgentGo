package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	frontendui "agentgo/frontend"
	"agentgo/internal/bridge"
	"agentgo/internal/config"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func hasArg(arg string) bool {
	for _, a := range os.Args[1:] {
		if a == arg || strings.TrimPrefix(a, "-") == strings.TrimPrefix(arg, "-") {
			return true
		}
	}
	return false
}

func handleAgentGoAPI(appService *bridge.AppService, w http.ResponseWriter, r *http.Request) {
	if !allowDesktopAPIRequest(w, r) {
		return
	}

	path := r.URL.Path
	switch path {
	case "/api/agentgo/status":
		status := appService.GetSystemStatus()
		_ = json.NewEncoder(w).Encode(status)
		return

	case "/api/agentgo/mode":
		var body struct {
			ModeId    string `json:"modeId"`
			SessionId string `json:"sessionId"`
		}
		if !decodeDesktopJSON(w, r, &body) {
			return
		}
		res := appService.SwitchIDEProfile(body.ModeId)
		_ = json.NewEncoder(w).Encode(res)
		return

	case "/api/agentgo/completion":
		var body struct {
			Prefix   string `json:"prefix"`
			Suffix   string `json:"suffix"`
			Language string `json:"language"`
		}
		if !decodeDesktopJSON(w, r, &body) {
			return
		}
		res := appService.GenerateCompletion(body.Prefix, body.Suffix, body.Language)
		_ = json.NewEncoder(w).Encode(res)
		return

	case "/api/agentgo/inline-edit":
		var body struct {
			FilePath     string `json:"filePath"`
			Language     string `json:"language"`
			SelectedText string `json:"selectedText"`
			Instruction  string `json:"instruction"`
		}
		if !decodeDesktopJSON(w, r, &body) {
			return
		}
		res := appService.GenerateInlineEdit(body.FilePath, body.Language, body.SelectedText, "", "", body.Instruction)
		_ = json.NewEncoder(w).Encode(res)
		return

	case "/api/agentgo/problem-fix":
		var body struct {
			FilePath     string `json:"filePath"`
			Code         string `json:"code"`
			ErrorMessage string `json:"errorMessage"`
		}
		if !decodeDesktopJSON(w, r, &body) {
			return
		}
		res := appService.FixProblem(body.FilePath, body.Code, body.ErrorMessage)
		_ = json.NewEncoder(w).Encode(res)
		return

	case "/api/agentgo/terminal-fix":
		var body struct {
			Command  string `json:"command"`
			Output   string `json:"output"`
			ExitCode int    `json:"exitCode"`
			Cwd      string `json:"cwd"`
		}
		if !decodeDesktopJSON(w, r, &body) {
			return
		}
		res := appService.TerminalQuickFix(body.Command, body.Output, body.ExitCode, body.Cwd)
		_ = json.NewEncoder(w).Encode(res)
		return

	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "not found"})
	}
}

func main() {
	serveMode := hasArg("--serve")
	var serveAddr, serveToken string
	if serveMode {
		if info, ok := existingServe(bridge.DefaultDataDir()); ok {
			log.Printf("[AgentGo] 已有常驻实例在运行: http://%s (pid=%d)，信息见 %s",
				info.Addr, info.PID, serveInfoPath(bridge.DefaultDataDir()))
			return
		}
		serveAddr, serveToken = prepareServeEnv()
	}

	rt, err := bridge.NewRuntime()
	if err != nil {
		fatalStart("AgentGo 启动失败", err)
	}

	defer rt.Close()

	// 自动适配环境变量中的 Azure OpenAI / GPT-6 Luna 密钥与端点
	if key, ep := os.Getenv("AZURE_OPENAI_API_KEY"), os.Getenv("AZURE_OPENAI_API_ENDPOINT"); key != "" && ep != "" {
		useLuna := hasArg("--luna") || config.UseLuna.On()
		cfg := rt.LLMConfig()
		if useLuna || cfg.APIKey == "" || cfg.APIBase == "https://api.openai.com/v1" {
			model := config.AzureDeployment.Get()
			if model == "" {
				model = "gpt-6-luna"
			}
			cfg.ContextWindow, cfg.MaxOutputTokens = 0, 0
			cfg.APIBase = strings.TrimRight(ep, "/") + "/openai/v1/"
			cfg.APIKey = key
			cfg.Model = model
			_ = rt.SetLLMConfig(cfg)
			log.Printf("[AgentGo] 已启用本地 Azure Luna: model=%s, api_base=%s", model, cfg.APIBase)
		}
		// Only Azure reasoning deployments need effort "none"; sending it to other
		// models (e.g. a DeepSeek relay) makes them echo prompt text into the reply.
		if m := strings.ToLower(rt.LLMConfig().Model); !config.ReasoningEffort.IsSet() &&
			(strings.Contains(m, "luna") || strings.Contains(m, "gpt-5")) {
			config.ReasoningEffort.Set("none")
		}
	}

	// 1. Check for ACP Server mode (stdio JSON-RPC 2.0 for OpenSumi / ACP Clients)
	if hasArg("--acp") || hasArg("acp") {
		if err := bridge.ServeACP(context.Background(), rt, os.Stdin, os.Stdout); err != nil && err != io.EOF {
			log.Fatalf("ACP server error: %v", err)
		}
		return
	}

	// 1b. Headless resident mode: gateway only, no window. Clients find it through serve.json.
	if serveMode {
		runServe(rt, serveAddr, serveToken)
		return
	}

	// 2. Vue3 TSX Desktop IDE (Wails + frontend as primary UI)
	targetURL := "/"
	if hasArg("--dev") || config.Dev.On() {
		vitePort := os.Getenv("WAILS_VITE_PORT")
		if vitePort == "" {
			vitePort = "9245"
		}
		targetURL = "http://127.0.0.1:" + vitePort
		log.Println("[AgentGo] 正在以 Dev Server 模式启动桌面窗口，目标地址:", targetURL)
	} else {
		log.Println("[AgentGo] 正在以单二进制自包含模式启动 Vue3 TSX 桌面 IDE...")
	}

	appService := bridge.NewAppService(rt)

	bundledHandler := application.BundledAssetFileServer(frontendui.Assets)
	assetHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wails/custom.js" {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("// wails custom\n"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/attachments/") {
			serveDesktopAttachment(w, r, filepath.Join(rt.DataDir(), "attachments"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/agentgo/") {
			handleAgentGoAPI(appService, w, r)
			return
		}
		bundledHandler.ServeHTTP(w, r)
	})

	app := application.New(application.Options{
		Name:        "AgentGo",
		Description: "AgentGo Desktop IDE (Vue3 TSX Monaco Core + Eino)",
		Services: []application.Service{
			application.NewService(appService),
		},
		Assets: application.AssetOptions{
			Handler: assetHandler,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:                  "AgentGo",
		Width:                  1280,
		Height:                 850,
		URL:                    targetURL,
		OpenInspectorOnStartup: hasArg("--dev") || config.Dev.On(),
		DevToolsEnabled:        true,
		// Without this the Wails runtime shows a "no drop" cursor for every file
		// dragged in from the file manager. With it, WebView2 hands us the real
		// paths of whatever lands on a data-file-drop-target area (the chat).
		EnableFileDrop: true,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})

	bridge.ForwardFileDrops(win)

	if err := app.Run(); err != nil {
		fatalStart("桌面窗口无法打开（常见原因：缺少 WebView2 运行时；没有它的电脑请改用浏览器版 AgentGo-web.exe）", err)
	}
}
