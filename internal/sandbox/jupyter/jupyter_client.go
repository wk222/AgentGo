package jupyter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// ConnectionInfo holds details for connecting to the Jupyter Kernel Gateway.
type ConnectionInfo struct {
	Host             string
	Port             int
	Token            string
	KernelName       string
	WaitReadyTimeout time.Duration
}

// Client acts as a client proxy communicating via REST and WebSocket with a Jupyter Kernel.
type Client struct {
	connectionInfo   ConnectionInfo
	baseURL          string
	httpClient       *http.Client
	kernelID         string
	ws               *websocket.Conn
	sessionID        string
	waitReadyTimeout time.Duration
}

type kernelSpec struct {
	DisplayName string `json:"display_name"`
	Language    string `json:"language"`
}

type kernelInfo struct {
	Name string     `json:"name"`
	Spec kernelSpec `json:"spec"`
}

type kernelSpecResponse struct {
	Specs map[string]kernelInfo `json:"kernelspecs"`
}

type executionMessage struct {
	Header struct {
		MsgType string `json:"msg_type"`
		MsgID   string `json:"msg_id"`
	} `json:"header"`
	Content      map[string]any `json:"content"`
	Metadata     map[string]any `json:"metadata"`
	ParentHeader struct {
		MsgID string `json:"msg_id"`
	} `json:"parent_header"`
}

// NewClient initializes rest specs, creates a kernel instance, and dials websocket.
func NewClient(info ConnectionInfo) (*Client, error) {
	baseURL := fmt.Sprintf("http://%s:%d", info.Host, info.Port)
	c := &Client{
		connectionInfo: info,
		baseURL:        baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		waitReadyTimeout: 10 * time.Second,
	}
	if info.WaitReadyTimeout > 0 {
		c.waitReadyTimeout = info.WaitReadyTimeout
	}

	available, err := c.listKernelSpecs()
	if err != nil {
		return nil, fmt.Errorf("list kernelspecs: %w", err)
	}

	if _, ok := available.Specs[info.KernelName]; !ok {
		// Fallback detection logic: if no python3 spec but we have specs, pick first one, or report error
		if len(available.Specs) == 0 {
			return nil, fmt.Errorf("no kernels available in jupyter specs")
		}
	}

	c.kernelID, err = c.startKernel(info.KernelName)
	if err != nil {
		return nil, fmt.Errorf("start kernel: %w", err)
	}

	wsURL := fmt.Sprintf("ws://%s:%d/api/kernels/%s/channels", info.Host, info.Port, c.kernelID)
	reqHeader := http.Header{}
	if info.Token != "" {
		reqHeader.Set("Authorization", "token "+info.Token)
	}

	dialer := websocket.DefaultDialer
	ws, _, err := dialer.Dial(wsURL, reqHeader)
	if err != nil {
		return nil, fmt.Errorf("ws dial %s: %w", wsURL, err)
	}

	c.ws = ws
	c.sessionID = uuid.NewString()

	ready, err := c.waitForReady()
	if err != nil {
		c.Close()
		return nil, err
	}
	if !ready {
		c.Close()
		return nil, fmt.Errorf("kernel info check timed out / failed")
	}

	return c, nil
}

func (c *Client) listKernelSpecs() (kernelSpecResponse, error) {
	url := fmt.Sprintf("%s/api/kernelspecs", c.baseURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return kernelSpecResponse{}, err
	}

	if c.connectionInfo.Token != "" {
		req.Header.Set("Authorization", "token "+c.connectionInfo.Token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return kernelSpecResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return kernelSpecResponse{}, fmt.Errorf("http error status: %s", resp.Status)
	}

	var res kernelSpecResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return kernelSpecResponse{}, err
	}
	return res, nil
}

func (c *Client) startKernel(name string) (string, error) {
	url := fmt.Sprintf("%s/api/kernels", c.baseURL)
	reqBody, _ := json.Marshal(map[string]string{"name": name})

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.connectionInfo.Token != "" {
		req.Header.Set("Authorization", "token "+c.connectionInfo.Token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed creating kernel: %s", resp.Status)
	}

	var res struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	return res.ID, nil
}

func (c *Client) waitForReady() (bool, error) {
	msgID, err := c.sendMessage(map[string]any{}, "shell", "kernel_info_request")
	if err != nil {
		return false, err
	}

	timeout := time.After(c.waitReadyTimeout)
	for {
		select {
		case <-timeout:
			return false, fmt.Errorf("waitForReady timeout")
		default:
		}

		var msg executionMessage
		if err := c.ws.ReadJSON(&msg); err != nil {
			return false, err
		}

		if msg.Header.MsgType == "kernel_info_reply" && msg.ParentHeader.MsgID == msgID {
			return true, nil
		}
	}
}

func (c *Client) sendMessage(content map[string]any, channel, msgType string) (string, error) {
	msgID := uuid.NewString()
	timestamp := time.Now().Format(time.RFC3339)
	payload := map[string]any{
		"header": map[string]any{
			"username": "agentgo",
			"version":  "5.0",
			"session":  c.sessionID,
			"msg_id":   msgID,
			"msg_type": msgType,
			"date":     timestamp,
		},
		"parent_header": map[string]any{},
		"metadata":      map[string]any{},
		"content":       content,
		"buffers":       []any{},
		"channel":       channel,
	}

	if c.ws == nil {
		return "", fmt.Errorf("websocket connection is closed")
	}

	if err := c.ws.WriteJSON(payload); err != nil {
		return "", err
	}
	return msgID, nil
}

// ExecuteCode runs Python code in the kernel and blocks until execution is idle.
func (c *Client) ExecuteCode(code string) (string, error) {
	code = silencePip(code)
	msgID, err := c.sendMessage(map[string]any{
		"code":             code,
		"silent":           false,
		"store_history":    true,
		"user_expressions": map[string]any{},
		"allow_stdin":      false,
		"stop_on_error":    true,
	}, "shell", "execute_request")
	if err != nil {
		return "", err
	}

	var outputs []string
	var errMsgs []string

	for {
		var msg executionMessage
		if err := c.ws.ReadJSON(&msg); err != nil {
			return "", err
		}

		if msg.ParentHeader.MsgID != msgID {
			continue
		}

		msgType := msg.Header.MsgType
		content := msg.Content

		if msgType == "status" && content["execution_state"] == "idle" {
			break
		}

		if msgType == "error" {
			if ename, ok := content["ename"].(string); ok {
				evalue, _ := content["evalue"].(string)
				errMsgs = append(errMsgs, fmt.Sprintf("%s: %s", ename, evalue))
			}
		}

		// Read stdout/stderr text
		if text, ok := content["text"].(string); ok {
			outputs = append(outputs, text)
		}
	}

	if len(errMsgs) > 0 {
		return strings.Join(outputs, "\n"), fmt.Errorf("%s", strings.Join(errMsgs, "\n"))
	}
	return strings.Join(outputs, "\n"), nil
}

// Close closes the WebSocket connection and releases REST client resources.
func (c *Client) Close() error {
	if c.ws != nil {
		return c.ws.Close()
	}
	return nil
}

func silencePip(code string) string {
	re, err := regexp.Compile(`(?m)^! ?pip install`)
	if err != nil {
		return code
	}
	lines := strings.Split(code, "\n")
	for i, line := range lines {
		if re.MatchString(line) && !strings.Contains(line, "-qqq") {
			matched := re.FindString(line)
			lines[i] = strings.Replace(line, matched, matched+" -qqq", 1)
		}
	}
	return strings.Join(lines, "\n")
}
