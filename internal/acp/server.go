package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// AgentHandler provides agent capabilities for ACP requests.
type AgentHandler interface {
	HandlePrompt(ctx context.Context, sessionID, prompt string, emit func(update SessionUpdate)) (string, error)
	CancelSession(sessionID string) bool
	NewSession(params NewSessionParams) (*NewSessionResult, error)
	SetSessionMode(sessionID, modeID string) error
	CloseSession(sessionID string) error

	// Extended IDE Native Fusion Methods
	GenerateInlineEdit(filePath, language, selectedText, contextBefore, contextAfter, instruction string) (map[string]any, error)
	GetSystemStatus() (map[string]any, error)
	TerminalQuickFix(command, output string, exitCode int, cwd string) (map[string]any, error)
	GenerateCompletion(prefix, suffix, language string) (map[string]any, error)
	FixProblem(filePath, code, errorMessage string) (map[string]any, error)
}

// Server processes ACP JSON-RPC requests over stdio or streams.
type Server struct {
	handler        AgentHandler
	in             io.Reader
	out            io.Writer
	mu             sync.Mutex
	sessionCounter int64
}

// NewServer creates a new ACP server.
func NewServer(handler AgentHandler, in io.Reader, out io.Writer) *Server {
	return &Server{
		handler: handler,
		in:      in,
		out:     out,
	}
}

// Serve reads requests line-by-line and dispatches responses.
func (s *Server) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(s.in)
	// Allow large requests up to 8MB
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 8*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32700, "Parse error", err.Error())
			continue
		}

		s.handleRequest(ctx, req)
	}

	return scanner.Err()
}

func (s *Server) handleRequest(ctx context.Context, req Request) {
	switch req.Method {
	case "initialize":
		s.sendResult(req.ID, InitializeResult{
			ProtocolVersion: 1,
			AgentCapabilities: map[string]any{
				"loadSession": true,
				"sessionCapabilities": map[string]any{
					"close": map[string]any{},
				},
				"mcpCapabilities": map[string]any{
					"acp":  true,
					"http": true,
				},
			},
			AgentInfo: EntityInfo{
				Name:    "AgentGo",
				Version: "0.10.0",
			},
			AuthMethods: []any{},
		})

	case "newSession", "session/new", "session/create":
		var p NewSessionParams
		_ = json.Unmarshal(req.Params, &p)
		if p.SessionID == "" {
			cnt := atomic.AddInt64(&s.sessionCounter, 1)
			p.SessionID = fmt.Sprintf("agentgo-acp-%d", cnt)
		}

		var res *NewSessionResult
		var err error
		if s.handler != nil {
			res, err = s.handler.NewSession(p)
		}
		if err != nil {
			s.sendError(req.ID, -32000, "Failed to create session", err.Error())
			return
		}
		if res == nil {
			res = &NewSessionResult{
				SessionID: p.SessionID,
				Modes: &SessionModes{
					CurrentModeID: "coder",
					AvailableModes: []ModeItem{
						{ID: "architect", Name: "Architect", Description: "系统设计与架构审查"},
						{ID: "coder", Name: "Coder", Description: "代码开发与缺陷修复"},
						{ID: "ask", Name: "Ask", Description: "代码探索与知识咨询"},
					},
				},
				ConfigOptions: []ConfigOptionItem{
					{
						ID:           "model",
						Name:         "Model",
						Type:         "select",
						Category:     "model",
						CurrentValue: "default",
						Options: []ConfigOptionEntry{
							{Value: "default", Name: "AgentGo Default"},
							{Value: "deepseek-chat", Name: "DeepSeek V3"},
							{Value: "claude-sonnet-4-5", Name: "Claude 3.7 Sonnet"},
						},
					},
				},
			}
		}
		s.sendResult(req.ID, res)

	case "loadSession", "session/load":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.sendResult(req.ID, map[string]any{
			"configOptions": []ConfigOptionItem{
				{
					ID:           "model",
					Name:         "Model",
					Type:         "select",
					Category:     "model",
					CurrentValue: "default",
				},
			},
			"modes": SessionModes{
				CurrentModeID: "coder",
				AvailableModes: []ModeItem{
					{ID: "architect", Name: "Architect"},
					{ID: "coder", Name: "Coder"},
					{ID: "ask", Name: "Ask"},
				},
			},
		})

	case "prompt", "session/prompt":
		var p PromptParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
		promptText := p.ExtractPromptText()
		if promptText == "" {
			s.sendError(req.ID, -32602, "Invalid params", "prompt is required")
			return
		}

		go func() {
			emit := func(update SessionUpdate) {
				// Standard ACP sessionUpdate notification
				s.sendNotification("sessionUpdate", SessionUpdateNotification{
					SessionID: p.SessionID,
					Update:    update,
				})
			}

			if s.handler == nil {
				s.sendError(req.ID, -32000, "Internal error", "no handler configured")
				return
			}

			_, err := s.handler.HandlePrompt(ctx, p.SessionID, promptText, emit)
			if err != nil {
				s.sendError(req.ID, -32000, "Execution failed", err.Error())
				return
			}

			s.sendResult(req.ID, PromptResult{
				StopReason: "end_turn",
			})
		}()

	case "cancel", "session/cancel":
		var p CancelParams
		_ = json.Unmarshal(req.Params, &p)
		success := false
		if s.handler != nil {
			success = s.handler.CancelSession(p.SessionID)
		}
		s.sendResult(req.ID, map[string]any{
			"cancelled": success,
		})

	case "closeSession", "session/close":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if s.handler != nil {
			_ = s.handler.CloseSession(p.SessionID)
		}
		s.sendResult(req.ID, map[string]any{})

	case "setSessionMode", "session/setMode":
		var p SetSessionModeParams
		_ = json.Unmarshal(req.Params, &p)
		if s.handler != nil {
			_ = s.handler.SetSessionMode(p.SessionID, p.ModeID)
		}
		s.sendResult(req.ID, map[string]any{})

	case "inlineEdit", "ide/inlineEdit":
		var p struct {
			FilePath      string `json:"filePath"`
			Language      string `json:"language"`
			SelectedText  string `json:"selectedText"`
			ContextBefore string `json:"contextBefore"`
			ContextAfter  string `json:"contextAfter"`
			Instruction   string `json:"instruction"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if s.handler != nil {
			res, err := s.handler.GenerateInlineEdit(p.FilePath, p.Language, p.SelectedText, p.ContextBefore, p.ContextAfter, p.Instruction)
			if err != nil {
				s.sendError(req.ID, -32000, "Inline edit failed", err.Error())
				return
			}
			s.sendResult(req.ID, res)
		} else {
			s.sendError(req.ID, -32000, "No handler configured", "")
		}

	case "getSystemStatus", "ide/systemStatus":
		if s.handler != nil {
			res, err := s.handler.GetSystemStatus()
			if err != nil {
				s.sendError(req.ID, -32000, "Failed to get system status", err.Error())
				return
			}
			s.sendResult(req.ID, res)
		} else {
			s.sendError(req.ID, -32000, "No handler configured", "")
		}

	case "terminalQuickFix", "ide/terminalQuickFix":
		var p struct {
			Command  string `json:"command"`
			Output   string `json:"output"`
			ExitCode int    `json:"exitCode"`
			CWD      string `json:"cwd"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if s.handler != nil {
			res, err := s.handler.TerminalQuickFix(p.Command, p.Output, p.ExitCode, p.CWD)
			if err != nil {
				s.sendError(req.ID, -32000, "Terminal quick fix failed", err.Error())
				return
			}
			s.sendResult(req.ID, res)
		} else {
			s.sendError(req.ID, -32000, "No handler configured", "")
		}

	case "completion", "ide/completion":
		var p struct {
			Prefix   string `json:"prefix"`
			Suffix   string `json:"suffix"`
			Language string `json:"language"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if s.handler != nil {
			res, err := s.handler.GenerateCompletion(p.Prefix, p.Suffix, p.Language)
			if err != nil {
				s.sendError(req.ID, -32000, "Completion failed", err.Error())
				return
			}
			s.sendResult(req.ID, res)
		} else {
			s.sendError(req.ID, -32000, "No handler configured", "")
		}

	case "problemFix", "ide/problemFix":
		var p struct {
			FilePath     string `json:"filePath"`
			Code         string `json:"code"`
			ErrorMessage string `json:"errorMessage"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if s.handler != nil {
			res, err := s.handler.FixProblem(p.FilePath, p.Code, p.ErrorMessage)
			if err != nil {
				s.sendError(req.ID, -32000, "Problem fix failed", err.Error())
				return
			}
			s.sendResult(req.ID, res)
		} else {
			s.sendError(req.ID, -32000, "No handler configured", "")
		}

	default:
		s.sendError(req.ID, -32601, "Method not found", fmt.Sprintf("unsupported method: %s", req.Method))
	}
}

func (s *Server) sendResult(id any, result any) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	s.writeEnvelope(resp)
}

func (s *Server) sendError(id any, code int, message string, data any) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &Error{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	s.writeEnvelope(resp)
}

func (s *Server) sendNotification(method string, params any) {
	notif := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	s.writeEnvelope(notif)
}

func (s *Server) writeEnvelope(v any) {
	bytes, err := json.Marshal(v)
	if err != nil {
		return
	}
	bytes = append(bytes, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.out.Write(bytes)
}
