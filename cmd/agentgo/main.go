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
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
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
		_ = json.NewDecoder(r.Body).Decode(&body)
		res := appService.SwitchIDEProfile(body.ModeId)
		_ = json.NewEncoder(w).Encode(res)
		return

	case "/api/agentgo/completion":
		var body struct {
			Prefix   string `json:"prefix"`
			Suffix   string `json:"suffix"`
			Language string `json:"language"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
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
		_ = json.NewDecoder(r.Body).Decode(&body)
		res := appService.GenerateInlineEdit(body.FilePath, body.Language, body.SelectedText, "", "", body.Instruction)
		_ = json.NewEncoder(w).Encode(res)
		return

	case "/api/agentgo/problem-fix":
		var body struct {
			FilePath     string `json:"filePath"`
			Code         string `json:"code"`
			ErrorMessage string `json:"errorMessage"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
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
		_ = json.NewDecoder(r.Body).Decode(&body)
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
		log.Fatal(err)
	}

	// 自动适配环境变量中的 Azure OpenAI / GPT-6 Luna 密钥与端点
	if key, ep := os.Getenv("AZURE_OPENAI_API_KEY"), os.Getenv("AZURE_OPENAI_API_ENDPOINT"); key != "" && ep != "" {
		useLuna := hasArg("--luna") || os.Getenv("AGENTGO_USE_LUNA") == "1"
		cfg := rt.LLMConfig()
		if useLuna || cfg.APIKey == "" || cfg.APIBase == "https://api.openai.com/v1" {
			model := os.Getenv("AGENTGO_AZURE_DEPLOYMENT")
			if model == "" {
				model = "gpt-6-luna"
			}
			cfg.APIBase = strings.TrimRight(ep, "/") + "/openai/v1/"
			cfg.APIKey = key
			cfg.Model = model
			_ = rt.SetLLMConfig(cfg)
			log.Printf("[AgentGo] 已启用本地 Azure Luna: model=%s, api_base=%s", model, cfg.APIBase)
		}
		if os.Getenv("AGENTGO_REASONING_EFFORT") == "" {
			_ = os.Setenv("AGENTGO_REASONING_EFFORT", "none")
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
	if hasArg("--dev") || os.Getenv("AGENTGO_DEV") == "1" {
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
			fileName := strings.TrimPrefix(r.URL.Path, "/attachments/")
			filePath := filepath.Join(rt.DataDir(), "attachments", fileName)
			http.ServeFile(w, r, filePath)
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

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:                  "AgentGo",
		Width:                  1280,
		Height:                 850,
		URL:                    targetURL,
		OpenInspectorOnStartup: hasArg("--dev") || os.Getenv("AGENTGO_DEV") == "1",
		DevToolsEnabled:        true,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
