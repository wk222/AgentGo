package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/computeruse"
	"agentgo/internal/config"
	"agentgo/internal/governance"
)

// computer_use: the model sees the screen and drives the mouse and keyboard.
//
// Off unless AGENTGO_COMPUTER_USE is set - enabling it is the user's consent,
// so individual clicks are not put through approval (a task is dozens of
// them). The brake is Ctrl+Alt+Esc, a real keypress the model cannot send
// (see computeruse.StartStopHotkey). The governance risk is declared HIGH for
// audit.

const computerUseDescription = `See and control the user's Windows desktop: take a screenshot, then click, type and press keys, like a person at the keyboard.

Loop: 1) action=screenshot  2) decide from the image  3) one action (click/type/key/scroll/drag)  4) every action returns a fresh screenshot - look at it before the next step.

Rules:
- Coordinates are pixels of the LATEST screenshot, origin top-left. Never guess coordinates from an old image.
- To read small text or hit a tiny target use action=zoom (x1,y1,x2,y2), then pass the returned zoom_id with x,y measured in the zoom image.
- Prefer keys over mouse when a shortcut exists (ctrl+l, alt+tab, enter).
- Prefer the accessibility actions over pixel clicks when the app exposes controls: ui_tree (window = part of the title; lists controls with ids and the center in screenshot pixels), then ui_press / ui_set_value / ui_read / ui_focus with element_id or role+name. They do not depend on layout, scaling or theme. ui_press clicks the center itself when a control has no invoke pattern. If ui_tree reports no tree (games, custom-drawn or web-content areas) fall back to screenshot + click.
- Use batch to run several steps with one screenshot at the end, only when nothing in between needs a look. Keys that delete, cut, close or lock need allow_destructive=true.
- Errors carry an "escalation": retry = fix the arguments and call again; use_different_tool = take a screenshot first; ask_user = tell the user what is wrong; stop = the user pressed Stop, do not call this tool again this turn.
- Do not type passwords or secrets. If a UAC prompt or the lock screen appears, ask the user.`

// computerUseBackend lazily starts either an in-process Controller or a
// sidecar connection, and re-launches the sidecar if its connection dies.
type computerUseBackend struct {
	mu      sync.Mutex
	cur     computeruse.Doer
	closeFn func()
	mk      func(ctx context.Context) (computeruse.Doer, func(), error)
}

func (b *computerUseBackend) Do(ctx context.Context, req computeruse.Request) (*computeruse.Result, error) {
	b.mu.Lock()
	if b.cur == nil {
		d, closeFn, err := b.mk(ctx)
		if err != nil {
			b.mu.Unlock()
			return nil, err
		}
		b.cur, b.closeFn = d, closeFn
	}
	d := b.cur
	b.mu.Unlock()

	res, err := d.Do(ctx, req)
	if err != nil {
		if e := computeruse.AsError(err); e.Code == computeruse.CodeSidecarUnavailable || e.Code == computeruse.CodeOutcomeUnknown {
			b.mu.Lock()
			if b.cur == d && b.closeFn != nil {
				b.closeFn()
				b.cur, b.closeFn = nil, nil // the next call starts a fresh session
			}
			b.mu.Unlock()
		}
	}
	return res, err
}

var (
	cuOnce    sync.Once
	cuBackend *computerUseBackend
)

func sharedComputerUseBackend() *computerUseBackend {
	cuOnce.Do(func() {
		cuBackend = &computerUseBackend{mk: func(ctx context.Context) (computeruse.Doer, func(), error) {
			if exe := config.ComputerUseSidecar.Get(); exe != "" {
				return startSidecar(ctx, exe)
			}
			ctl := computeruse.New(computeruse.NewSubstrate(), computeruse.WithAccessibility(computeruse.NewAccessibility()))
			stopKey, err := computeruse.StartStopHotkey(ctl)
			if err != nil {
				// Not fatal, but the user must know there is no brake.
				fmt.Fprintf(os.Stderr, "computer_use: stop hotkey unavailable: %v\n", err)
			}
			return ctl, stopKey, nil
		}}
	})
	return cuBackend
}

func startSidecar(ctx context.Context, exe string) (computeruse.Doer, func(), error) {
	if c, err := computeruse.Connect(ctx); err == nil { // one is already running (maybe elevated)
		return c, func() { _ = c.Close() }, nil
	}
	c, cmd, err := computeruse.LaunchSidecar(ctx, exe)
	if err != nil {
		return nil, nil, err
	}
	return c, func() {
		_ = c.Close()
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}, nil
}

// RegisterComputerUseTool registers `computer_use` when AGENTGO_COMPUTER_USE
// is on and this build has a desktop substrate. It is safe to call repeatedly.
func RegisterComputerUseTool(r *Registry) error {
	if !config.ComputerUse.On() || !computeruse.Supported() {
		return nil
	}
	governance.RegisterToolTraits("computer_use", governance.ToolTraits{
		Source: "computeruse", Risk: governance.RiskHigh,
	})
	backend := sharedComputerUseBackend()
	t, err := utils.InferEnhancedTool("computer_use", computerUseDescription,
		func(ctx context.Context, in computeruse.Request) (*schema.ToolResult, error) {
			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			return computerUseToolResult(backend.Do(ctx, in)), nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

// computerUseToolResult turns an action outcome into a tool result. Failures
// are returned as data, not as a Go error: the model has to read the
// escalation, and a tool error would abort the run instead.
func computerUseToolResult(res *computeruse.Result, err error) *schema.ToolResult {
	if err != nil {
		b, _ := json.Marshal(map[string]any{"error": computeruse.AsError(err)})
		return &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: string(b)}}}
	}
	text := res.Text
	if len(res.PNG) > 0 {
		// Not every provider accepts images inside a tool result. The file lets
		// the model fall back to the vision_advisor tool instead of going blind.
		if p := saveScreenshot(res.PNG); p != "" {
			text += "\nScreenshot file: " + p
		}
	}
	parts := []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: text}}
	if len(res.PNG) > 0 {
		b64 := base64.StdEncoding.EncodeToString(res.PNG)
		parts = append(parts, schema.ToolOutputPart{
			Type: schema.ToolPartTypeImage,
			Image: &schema.ToolOutputImage{MessagePartCommon: schema.MessagePartCommon{
				Base64Data: &b64, MIMEType: "image/png",
			}},
		})
	}
	return &schema.ToolResult{Parts: parts}
}

var (
	shotMu  sync.Mutex
	shotSeq int
)

// saveScreenshot keeps the last few screenshots on disk (a small ring, so it
// never grows). Set AGENTGO_COMPUTER_USE_DIR to put them off the system drive.
func saveScreenshot(png []byte) string {
	dir := config.ComputerUseDir.Get()
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "agentgo-computeruse")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	shotMu.Lock()
	shotSeq = (shotSeq + 1) % 6
	p := filepath.Join(dir, fmt.Sprintf("shot-%d.png", shotSeq))
	shotMu.Unlock()
	if err := os.WriteFile(p, png, 0o600); err != nil {
		return ""
	}
	return p
}
