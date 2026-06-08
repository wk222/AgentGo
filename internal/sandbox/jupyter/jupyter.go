package jupyter

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// JupyterExecutor spawns and manages a Jupyter kernelgateway background server and WebSocket client.
type JupyterExecutor struct {
	mu           sync.Mutex
	ip           string
	port         int
	token        string
	kernelName   time.Duration // unused semantic trick or configuration field
	kernelStr    string
	startTimeout time.Duration
	subprocess   *exec.Cmd
	client       *Client
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewJupyterExecutor starts jupyter gateway server subprocess on the specified port.
func NewJupyterExecutor(kernelStr string, port int) (*JupyterExecutor, error) {
	if err := checkJupyterGateway(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	token := generateToken()

	je := &JupyterExecutor{
		ip:           "127.0.0.1",
		port:         port,
		token:        token,
		kernelStr:    kernelStr,
		startTimeout: 15 * time.Second,
		ctx:          ctx,
		cancel:       cancel,
	}

	args := []string{
		"-m", "jupyter", "kernelgateway",
		"--KernelGatewayApp.ip", je.ip,
		"--KernelGatewayApp.auth_token", je.token,
		"--JupyterApp.answer_yes", "true",
		"--JupyterWebsocketPersonality.list_kernels", "true",
		"--KernelGatewayApp.port", strconv.Itoa(je.port),
		"--KernelGatewayApp.port_retries", "0",
	}

	cmd := exec.CommandContext(ctx, "python", args...)
	// Combined output handles stdout/stderr together
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	je.subprocess = cmd

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed starting jupyter gateway: %w", err)
	}

	readyCh := make(chan bool)
	errCh := make(chan error)

	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		timeout := time.After(je.startTimeout)

		for {
			select {
			case <-timeout:
				errCh <- fmt.Errorf("timed out waiting for jupyter gateway: %s", outBuf.String())
				return
			case <-ticker.C:
				if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
					errCh <- fmt.Errorf("jupyter gateway exited early: %s", outBuf.String())
					return
				}
				if strings.Contains(outBuf.String(), "is available at") {
					readyCh <- true
					return
				}
			}
		}
	}()

	select {
	case err := <-errCh:
		je.Close()
		return nil, err
	case <-readyCh:
		cli, err := NewClient(ConnectionInfo{
			Host:             je.ip,
			Port:             je.port,
			Token:            je.token,
			KernelName:       je.kernelStr,
			WaitReadyTimeout: 10 * time.Second,
		})
		if err != nil {
			je.Close()
			return nil, err
		}
		je.client = cli
		return je, nil
	}
}

// Execute submits code for execution to the Jupyter client connection.
func (je *JupyterExecutor) Execute(code string) (string, error) {
	je.mu.Lock()
	defer je.mu.Unlock()

	if je.client == nil {
		return "", fmt.Errorf("jupyter client is not initialized")
	}
	return je.client.ExecuteCode(code)
}

// Close kills the subprocess and WebSocket connections.
func (je *JupyterExecutor) Close() error {
	je.mu.Lock()
	defer je.mu.Unlock()

	je.cancel()

	if je.client != nil {
		_ = je.client.Close()
	}

	if je.subprocess != nil && je.subprocess.Process != nil {
		_ = je.subprocess.Process.Kill()
		_ = je.subprocess.Wait()
	}
	return nil
}

func checkJupyterGateway() error {
	cmd := exec.Command("python", "-m", "jupyter", "kernelgateway", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Jupyter gateway server is not installed. Please install it with `pip install jupyter_kernel_gateway`")
	}
	return nil
}

func generateToken() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 24)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := range b {
		b[i] = charset[r.Intn(len(charset))]
	}
	return string(b)
}
