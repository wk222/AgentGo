package crushengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"agentgo/internal/engine"
)

const (
	cancelGrace     = 15 * time.Second // wait for Crush to acknowledge a cancel
	permissionGrant = 10 * time.Second
)

// drive sends the prompt and pumps SSE events until this run's run_complete.
func (e *Engine) drive(ctx context.Context, cl *Client, wsID, sid, runID, prompt string, em engine.Emitter) (engine.RunResult, error) {
	// The stream must outlive ctx: after a cancel we still want the
	// run_complete that confirms Crush stopped.
	streamCtx, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	events, streamErr, err := cl.Stream(streamCtx, wsID)
	if err != nil {
		return engine.RunResult{}, fmt.Errorf("open crush event stream: %w", err)
	}

	runCtx, endRun := context.WithCancel(context.Background())
	defer endRun() // releases blocked Approvers when the run ends

	em.Status("crush_started", map[string]any{"crush_session": sid, "workspace_id": wsID})
	if err := cl.SendMessage(ctx, wsID, sid, runID, prompt); err != nil {
		if ctx.Err() != nil {
			// Cancelled while the prompt was in flight: Crush may or may not
			// have accepted it. Best-effort cancel, and report a clean cancel.
			cctx, c := context.WithTimeout(context.Background(), 5*time.Second)
			_ = cl.CancelSession(cctx, wsID, sid)
			c()
			return engine.RunResult{Error: "cancelled"}, nil
		}
		return engine.RunResult{}, fmt.Errorf("send prompt to crush: %w", err)
	}

	tr := newTranslator(sid, em)
	seenPerm := map[string]bool{}
	ctxDone := ctx.Done()
	var graceTimer <-chan time.Time
	cancelled := false

	for {
		select {
		case <-ctxDone:
			ctxDone = nil // fire once
			cancelled = true
			cctx, c := context.WithTimeout(context.Background(), 5*time.Second)
			_ = cl.CancelSession(cctx, wsID, sid)
			c()
			graceTimer = time.After(cancelGrace)

		case <-graceTimer:
			return engine.RunResult{Error: "cancelled"}, nil

		case env, ok := <-events:
			if !ok {
				if cancelled {
					return engine.RunResult{Error: "cancelled"}, nil
				}
				if e.sup != nil && !e.sup.Alive() {
					return engine.RunResult{}, errors.New("crush server exited during run")
				}
				if err := streamErr(); err != nil {
					return engine.RunResult{}, fmt.Errorf("crush event stream lost: %w", err)
				}
				return engine.RunResult{}, errors.New("crush event stream closed before run completed")
			}
			if res, done := e.handle(runCtx, cl, wsID, sid, runID, env, tr, em, seenPerm, cancelled); done {
				return res, nil
			}
		}
	}
}

// handle processes one SSE record; done=true means the run finished.
func (e *Engine) handle(runCtx context.Context, cl *Client, wsID, sid, runID string, env envelope,
	tr *translator, em engine.Emitter, seenPerm map[string]bool, cancelled bool,
) (engine.RunResult, bool) {
	switch env.Type {
	case payloadMessage:
		var ev pubEvent[wireMessage]
		if json.Unmarshal(env.Payload, &ev) == nil {
			tr.onMessage(ev.Payload)
		}
	case payloadFile:
		var ev pubEvent[wireFile]
		if json.Unmarshal(env.Payload, &ev) == nil {
			tr.onFile(ev.Payload)
		}
	case payloadPermissionRequest:
		var ev pubEvent[wirePermission]
		if json.Unmarshal(env.Payload, &ev) == nil && ev.Payload.SessionID == sid &&
			ev.Payload.ID != "" && !seenPerm[ev.Payload.ID] {
			seenPerm[ev.Payload.ID] = true
			e.askPermission(runCtx, cl, wsID, ev.Payload, em, cancelled)
		}
	case payloadQuestionRequest:
		var ev pubEvent[wireQuestion]
		if json.Unmarshal(env.Payload, &ev) == nil && ev.Payload.SessionID == sid {
			// No UI to answer the model's clarifying question: dismiss so the
			// run continues instead of hanging.
			em.Status("question_dismissed", map[string]any{"id": ev.Payload.ID})
			go func() {
				c, cancel := context.WithTimeout(context.Background(), permissionGrant)
				defer cancel()
				_ = cl.CancelQuestion(c, wsID)
			}()
		}
	case payloadRunComplete:
		var ev pubEvent[wireRunComplete]
		if json.Unmarshal(env.Payload, &ev) != nil {
			return engine.RunResult{}, false
		}
		rc := ev.Payload
		if rc.RunID != runID && !(rc.RunID == "" && rc.SessionID == sid) {
			return engine.RunResult{}, false // another turn's completion
		}
		res := engine.RunResult{}
		if rc.Text != "" {
			res.Messages = []engine.Message{{Role: "assistant", Type: "text", Content: rc.Text}}
		}
		switch {
		case rc.Cancelled:
			res.Error = "cancelled"
		case rc.Error != "":
			res.Error = rc.Error
		}
		return res, true
	}
	return engine.RunResult{}, false
}

// askPermission surfaces the request and resolves it asynchronously.
func (e *Engine) askPermission(runCtx context.Context, cl *Client, wsID string, p wirePermission, em engine.Emitter, cancelled bool) {
	em.ApprovalRequest(p.ID, p.ToolName, string(p.Params), p.Description)
	go func() {
		allow := e.cfg.AutoApprove
		if e.cfg.Approver != nil && !cancelled {
			allow = e.cfg.Approver(runCtx, PermissionRequest{
				ID: p.ID, SessionID: p.SessionID, ToolName: p.ToolName,
				Description: p.Description, Action: p.Action, Path: p.Path, Params: p.Params,
			})
		}
		if cancelled || runCtx.Err() != nil {
			allow = false
		}
		action := permDeny
		if allow {
			action = permAllow
		}
		c, cancel := context.WithTimeout(context.Background(), permissionGrant)
		defer cancel()
		_ = cl.GrantPermission(c, wsID, p, action)
	}()
}
