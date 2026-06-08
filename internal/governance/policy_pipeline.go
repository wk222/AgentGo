package governance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
)

// ToolPolicyContext carries per-invocation state for staged evaluation.
type ToolPolicyContext struct {
	ToolName    string
	Arguments   string
	IsDynamic   bool
	SessionID   string
	RecentCalls int
	Control     ControlPolicy
	AllowedRoot string
}

// PolicyStage is one step in the PyBot-style tool_policy_pipeline.
type PolicyStage interface {
	Name() string
	Evaluate(ctx ToolPolicyContext) *ToolControlDecision
}

// ToolPolicyPipeline runs stages and merges risk / approval tags.
type ToolPolicyPipeline struct {
	stages []PolicyStage
}

func NewToolPolicyPipeline(stages ...PolicyStage) *ToolPolicyPipeline {
	return &ToolPolicyPipeline{stages: stages}
}

func (p *ToolPolicyPipeline) Evaluate(ctx ToolPolicyContext) ToolControlDecision {
	if p == nil || len(p.stages) == 0 {
		return ctx.Control.EvaluateToolCall(ctx.ToolName, ctx.IsDynamic)
	}
	var tags []string
	risk := RiskLow
	approvalRequired := false
	var approvalReason string

	for _, stage := range p.stages {
		if stage == nil {
			continue
		}
		dec := stage.Evaluate(ctx)
		if dec == nil {
			continue
		}
		risk = maxRisk(risk, dec.Risk)
		tags = append(tags, dec.ControlTags...)
		if !dec.Allowed {
			dec.ControlTags = dedupeTags(append(tags, dec.ControlTags...))
			dec.Risk = risk
			return *dec
		}
		if dec.RequiresApproval {
			approvalRequired = true
			if dec.Reason != "" {
				approvalReason = dec.Reason
			}
		}
	}
	base := ctx.Control.EvaluateToolCall(ctx.ToolName, ctx.IsDynamic)
	risk = maxRisk(risk, base.Risk)
	tags = dedupeTags(append(tags, base.ControlTags...))
	if !base.Allowed {
		base.ControlTags = tags
		base.Risk = risk
		return base
	}
	if base.RequiresApproval {
		approvalRequired = true
	}
	return ToolControlDecision{
		Allowed:          true,
		Risk:             risk,
		RequiresApproval: approvalRequired,
		Reason:           approvalReason,
		ControlTags:      tags,
	}
}

func (p *ToolPolicyPipeline) Describe() []string {
	out := make([]string, 0, len(p.stages))
	for _, s := range p.stages {
		if s != nil {
			out = append(out, s.Name())
		}
	}
	return out
}

// PathPolicyStage blocks path traversal and paths outside workspace root.
type PathPolicyStage struct{}

func (PathPolicyStage) Name() string { return "path_policy" }

func (PathPolicyStage) Evaluate(ctx ToolPolicyContext) *ToolControlDecision {
	root := strings.TrimSpace(ctx.AllowedRoot)
	if root == "" {
		return nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	args := parseToolArgsJSON(ctx.Arguments)
	for key, val := range args {
		if !isPathArgKey(key) {
			continue
		}
		for _, item := range iterPathValues(val) {
			if strings.Contains(item, "..") {
				return &ToolControlDecision{
					Allowed: false, Risk: RiskCritical,
					Reason: "路径参数包含目录穿越: " + key,
					ControlTags: []string{"path-policy", "path-traversal"},
				}
			}
			p := filepath.Clean(item)
			if !filepath.IsAbs(p) {
				p = filepath.Join(absRoot, p)
			}
			abs, err := filepath.Abs(p)
			if err != nil {
				continue
			}
			if !pathUnderRoot(abs, absRoot) {
				return &ToolControlDecision{
					Allowed: false, Risk: RiskCritical,
					Reason: "路径超出工作区根目录: " + item,
					ControlTags: []string{"path-policy", "path-outside-root"},
				}
			}
		}
	}
	return nil
}

// RateLimitStage enforces per-session per-tool call caps.
type RateLimitStage struct {
	Tracker *ToolCallTracker
}

func (RateLimitStage) Name() string { return "rate_limit" }

func (s RateLimitStage) Evaluate(ctx ToolPolicyContext) *ToolControlDecision {
	max := ctx.Control.MaxCallsPerTool
	if max <= 0 {
		return nil
	}
	recent := ctx.RecentCalls
	if s.Tracker != nil {
		recent = s.Tracker.RecentCount(ctx.SessionID, ctx.ToolName)
	}
	if recent < max {
		return nil
	}
	return &ToolControlDecision{
		Allowed: false, Risk: RiskHigh,
		Reason: "工具调用已达速率上限",
		ControlTags: []string{"rate-limit"},
	}
}

// StuckLoopStage blocks when the same tool repeats too often in one session.
type StuckLoopStage struct {
	Tracker *ToolCallTracker
}

func (StuckLoopStage) Name() string { return "stuck_loop" }

func (s StuckLoopStage) Evaluate(ctx ToolPolicyContext) *ToolControlDecision {
	kill := ctx.Control.StuckLoopKillThreshold
	if kill <= 0 || s.Tracker == nil {
		return nil
	}
	n := s.Tracker.RecentCount(ctx.SessionID, ctx.ToolName)
	if n < kill {
		return nil
	}
	return &ToolControlDecision{
		Allowed: false, Risk: RiskCritical,
		Reason: "检测到工具循环调用，已阻断",
		ControlTags: []string{"stuck-loop", "kill"},
	}
}

// BashPatternStage rejects obviously destructive shell snippets.
type BashPatternStage struct{}

func (BashPatternStage) Name() string { return "bash_pattern" }

func (BashPatternStage) Evaluate(ctx ToolPolicyContext) *ToolControlDecision {
	if ctx.ToolName != "execute_bash" {
		return nil
	}
	lower := strings.ToLower(ctx.Arguments)
	deny := []string{"rm -rf /", "format c:", ":(){ :|:& };:", "mkfs.", "dd if=/dev/zero"}
	for _, p := range deny {
		if strings.Contains(lower, p) {
			return &ToolControlDecision{
				Allowed: false, Risk: RiskCritical,
				Reason: "shell 命令命中高危模式",
				ControlTags: []string{"bash-pattern"},
			}
		}
	}
	return nil
}

// BuildDefaultToolPolicyPipeline constructs the standard multi-stage pipeline.
func BuildDefaultToolPolicyPipeline(policy Policy, tracker *ToolCallTracker) *ToolPolicyPipeline {
	return NewToolPolicyPipeline(
		PathPolicyStage{},
		BashPatternStage{},
		RateLimitStage{Tracker: tracker},
		StuckLoopStage{Tracker: tracker},
	)
}

func maxRisk(a, b RiskLevel) RiskLevel {
	order := map[RiskLevel]int{RiskLow: 0, RiskMedium: 1, RiskHigh: 2, RiskCritical: 3}
	if order[a] >= order[b] {
		return a
	}
	return b
}

func dedupeTags(in []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(in))
	for _, t := range in {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func isPathArgKey(key string) bool {
	switch strings.ToLower(key) {
	case "path", "file_path", "target_path", "directory", "dir", "workspace_dir", "cwd":
		return true
	default:
		return false
	}
}

func parseToolArgsJSON(raw string) map[string]any {
	out := make(map[string]any)
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func iterPathValues(v any) []string {
	switch t := v.(type) {
	case string:
		if strings.TrimSpace(t) != "" {
			return []string{t}
		}
	case []any:
		var items []string
		for _, it := range t {
			items = append(items, iterPathValues(it)...)
		}
		return items
	}
	return nil
}

func pathUnderRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// PipelineContext builds evaluation context from Go context + policy.
func PipelineContext(goCtx context.Context, policy Policy, toolName, args string, isDynamic bool) ToolPolicyContext {
	return ToolPolicyContext{
		ToolName:    toolName,
		Arguments:   args,
		IsDynamic:   isDynamic,
		SessionID:   SessionIDFromContext(goCtx),
		Control:     policy.NormalizeControl(),
		AllowedRoot: policy.WorkspaceRoot,
	}
}
