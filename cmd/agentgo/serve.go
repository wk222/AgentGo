package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"agentgo/internal/bridge"
)

// serveInfo is written next to the database so clients (VS Code extension, tray, scripts)
// can discover a running headless AgentGo without guessing port/token.
type serveInfo struct {
	Addr      string `json:"addr"`
	Token     string `json:"token"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
}

const defaultServeAddr = "127.0.0.1:8787"

func serveInfoPath(dataDir string) string { return filepath.Join(dataDir, "serve.json") }

func newToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "agentgo-" + time.Now().Format("20060102150405")
	}
	return hex.EncodeToString(b)
}

// prepareServeEnv must run BEFORE bridge.NewRuntime(): the gateway plugin reads these env vars at boot.
// It never binds beyond loopback unless the user explicitly set AGENTGO_GATEWAY_ADDR.
func prepareServeEnv() (addr, token string) {
	addr = strings.TrimSpace(os.Getenv("AGENTGO_GATEWAY_ADDR"))
	if addr == "" {
		if p := strings.TrimSpace(os.Getenv("AGENTGO_GATEWAY_PORT")); p != "" {
			addr = "127.0.0.1:" + p
		} else {
			addr = defaultServeAddr
		}
		_ = os.Setenv("AGENTGO_GATEWAY_ADDR", addr)
	}
	token = strings.TrimSpace(os.Getenv("AGENTGO_GATEWAY_TOKEN"))
	if token == "" {
		token = newToken()
		_ = os.Setenv("AGENTGO_GATEWAY_TOKEN", token)
	}
	return addr, token
}

// existingServe returns the info of an already running headless instance, if its /health answers.
func existingServe(dataDir string) (*serveInfo, bool) {
	raw, err := os.ReadFile(serveInfoPath(dataDir))
	if err != nil {
		return nil, false
	}
	var info serveInfo
	if json.Unmarshal(raw, &info) != nil || info.Addr == "" {
		return nil, false
	}
	cli := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := cli.Get("http://" + info.Addr + "/health")
	if err != nil {
		return nil, false
	}
	_ = resp.Body.Close()
	return &info, resp.StatusCode == http.StatusOK
}

// runServe blocks until SIGINT/SIGTERM, keeping the runtime (and its gateway/scheduler/taskhub) alive.
func runServe(rt *bridge.Runtime, addr, token string) {
	dataDir := rt.DataDir()
	info := serveInfo{Addr: addr, Token: token, PID: os.Getpid(), StartedAt: time.Now().Format(time.RFC3339)}
	if err := writeServeInfo(dataDir, info); err != nil {
		log.Printf("[AgentGo] 写入 serve.json 失败: %v", err)
	}
	log.Printf("[AgentGo] 常驻模式已启动: http://%s ，连接信息(含令牌)见 %s", addr, serveInfoPath(dataDir))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Println("[AgentGo] 收到退出信号，正在关闭…")
	_ = os.Remove(serveInfoPath(dataDir))
	if err := rt.Close(); err != nil {
		log.Printf("[AgentGo] 关闭运行时: %v", err)
	}
}

func writeServeInfo(dataDir string, info serveInfo) error {
	raw, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	// 0600: the file contains the bearer token.
	return os.WriteFile(serveInfoPath(dataDir), raw, 0o600)
}
