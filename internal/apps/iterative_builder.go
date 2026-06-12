package apps

import (
	"context"
	"encoding/json"
	"fmt"
)

// IterativeBuildOptions configures build_inner_app_iteratively.
type IterativeBuildOptions struct {
	Name          string
	DisplayName   string
	Description   string
	Mode          string
	WorkflowID    string
	SystemPrompt  string
	MaxIterations int
	Overwrite     bool
}

// IterativeBuildResult aggregates scaffold + verify loop outcome.
type IterativeBuildResult struct {
	Success     bool           `json:"success"`
	AppName     string         `json:"app_name,omitempty"`
	Scaffold    ScaffoldResult `json:"scaffold,omitempty"`
	Iterations  int            `json:"iterations"`
	FinalVerify VerifyResult   `json:"final_verify"`
	Repairs     []string       `json:"repairs,omitempty"`
	Message     string         `json:"message,omitempty"`
	Error       string         `json:"error,omitempty"`
}

// IterativeBuilder runs scaffold → verify → auto-fix loops (PyBot build_app_iteratively subset).
type IterativeBuilder struct {
	Scaffolder *Scaffolder
	Pinger     AppPinger
}

func (b *IterativeBuilder) Build(ctx context.Context, opt IterativeBuildOptions) IterativeBuildResult {
	if b == nil || b.Scaffolder == nil {
		return IterativeBuildResult{Success: false, Error: "scaffolder not configured"}
	}
	maxIter := opt.MaxIterations
	if maxIter <= 0 {
		maxIter = 3
	}
	if maxIter > 8 {
		maxIter = 8
	}
	scRes, err := b.Scaffolder.Scaffold(ctx, ScaffoldOptions{
		Name: opt.Name, DisplayName: opt.DisplayName, Description: opt.Description,
		Mode: opt.Mode, WorkflowID: opt.WorkflowID, SystemPrompt: opt.SystemPrompt,
		Overwrite: opt.Overwrite,
	})
	out := IterativeBuildResult{
		AppName: scRes.AppName, Scaffold: scRes,
	}
	if err != nil && !scRes.Success {
		out.Success = false
		out.Error = scRes.Error
		return out
	}
	name := scRes.AppName
	root := b.Scaffolder.AppsRoot
	var repairs []string
	var final VerifyResult
	for i := 0; i < maxIter; i++ {
		out.Iterations = i + 1
		final = VerifyBundle(ctx, root, name, b.Pinger)
		if final.Success {
			out.Success = true
			out.FinalVerify = final
			out.Repairs = repairs
			out.Message = fmt.Sprintf("built in %d iteration(s)", out.Iterations)
			return out
		}
		fixed, fixErr := AutoFixBundle(root, name)
		if fixErr != nil {
			out.Error = fixErr.Error()
			break
		}
		if fixed {
			repairs = append(repairs, fmt.Sprintf("iter %d: injected agentgo-app-helpers into index.html", i+1))
			continue
		}
		break
	}
	final = VerifyBundle(ctx, root, name, b.Pinger)
	out.FinalVerify = final
	out.Repairs = repairs
	out.Success = final.Success
	if out.Success {
		out.Message = "build complete after auto-repair"
	} else {
		out.Message = "scaffold ok; manual fix via update_inner_app_file or agent"
		if len(final.Issues) > 0 {
			b, _ := json.Marshal(final.Issues)
			out.Error = "remaining issues: " + string(b)
		}
	}
	return out
}
