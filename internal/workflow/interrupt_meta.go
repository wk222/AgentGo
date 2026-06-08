package workflow

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"

	"agentgo/internal/governance"
)

// WorkflowInterruptKind classifies compose pause points for bridge / UI routing.
type WorkflowInterruptKind string

const (
	InterruptToolApproval WorkflowInterruptKind = "tool_approval"
	InterruptWaitSignal   WorkflowInterruptKind = "wait_signal"
	InterruptAskUser      WorkflowInterruptKind = "ask_user"
	InterruptGeneric      WorkflowInterruptKind = "generic"
)

// InterruptMeta is the normalized view of a workflow InterruptError for HITL resume.
type InterruptMeta struct {
	Kind         WorkflowInterruptKind
	InterruptID  string
	ApprovalID   string
	ToolName     string
	Arguments    string
	Prompt       string
	Signal       string
}

// MetaFromInterrupt derives UI/bridge routing metadata from a workflow interrupt.
func MetaFromInterrupt(ie *InterruptError) InterruptMeta {
	if ie == nil {
		return InterruptMeta{Kind: InterruptGeneric}
	}
	meta := MetaFromInfo(ie.Info)
	if strings.TrimSpace(meta.InterruptID) == "" {
		meta.InterruptID = strings.TrimSpace(ie.InterruptID)
	}
	return meta
}

// MetaFromInfo parses compose interrupt contexts (tool approval, wait_signal, ask_user).
func MetaFromInfo(info *compose.InterruptInfo) InterruptMeta {
	if info == nil || len(info.InterruptContexts) == 0 {
		return InterruptMeta{Kind: InterruptGeneric}
	}
	ic := pickRootInterruptCtx(info.InterruptContexts)
	if ic == nil {
		return InterruptMeta{Kind: InterruptGeneric}
	}
	return metaFromPauseInfo(ic.Info, ic.ID)
}

func pickRootInterruptCtx(contexts []*compose.InterruptCtx) *compose.InterruptCtx {
	var fallback *compose.InterruptCtx
	for _, ic := range contexts {
		if ic == nil {
			continue
		}
		if fallback == nil {
			fallback = ic
		}
		if ic.IsRootCause {
			return ic
		}
	}
	return fallback
}

func metaFromPauseInfo(info any, interruptID string) InterruptMeta {
	interruptID = strings.TrimSpace(interruptID)
	switch v := info.(type) {
	case governance.ToolApprovalPause:
		return InterruptMeta{
			Kind:        InterruptToolApproval,
			InterruptID: interruptID,
			ApprovalID:  v.ApprovalID,
			ToolName:    v.ToolName,
			Arguments:   v.Arguments,
		}
	case map[string]any:
		if id, ok := v["approval_id"].(string); ok && strings.TrimSpace(id) != "" {
			return InterruptMeta{
				Kind:        InterruptToolApproval,
				InterruptID: interruptID,
				ApprovalID:  id,
				ToolName:    mapStr(v, "tool"),
				Arguments:   mapStr(v, "arguments"),
			}
		}
		if strings.EqualFold(mapStr(v, "kind"), "ask_user") {
			return InterruptMeta{
				Kind:        InterruptAskUser,
				InterruptID: interruptID,
				Prompt:      mapStr(v, "prompt"),
			}
		}
		if sig := mapStr(v, "signal"); sig != "" {
			return InterruptMeta{
				Kind:        InterruptWaitSignal,
				InterruptID: interruptID,
				Signal:      sig,
			}
		}
	case askUserState:
		return InterruptMeta{
			Kind:        InterruptAskUser,
			InterruptID: interruptID,
			Prompt:      v.Prompt,
		}
	case waitSignalState:
		return InterruptMeta{
			Kind:        InterruptWaitSignal,
			InterruptID: interruptID,
			Signal:      v.Signal,
		}
	}
	return InterruptMeta{Kind: InterruptGeneric, InterruptID: interruptID}
}

func mapStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(fmt.Sprint(m[key]))
}
