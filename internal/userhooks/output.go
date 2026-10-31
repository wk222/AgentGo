package userhooks

import (
	"encoding/json"
	"strings"
)

// verdict is what a script that exited with status 0 asked for in its stdout.
type verdict struct {
	block   bool
	reason  string
	context string   // text to give the model along with the message
	notes   []string // what the script said that is only reported, or not supported yet
}

// scriptOutput is the JSON a script may print. The field names are Claude
// Code's and Codex's, so scripts written for either are read the same way.
type scriptOutput struct {
	Continue      *bool  `json:"continue"`
	StopReason    string `json:"stopReason"`
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
	SystemMessage string `json:"systemMessage"`
	Specific      struct {
		PermissionDecision       string          `json:"permissionDecision"`
		PermissionDecisionReason string          `json:"permissionDecisionReason"`
		AdditionalContext        string          `json:"additionalContext"`
		UpdatedInput             json.RawMessage `json:"updatedInput"`
	} `json:"hookSpecificOutput"`
}

// readVerdict interprets what a script printed when it exited with status 0.
//
//   - Nothing, or text that is not JSON, asks for nothing; for UserPromptSubmit
//     the plain text is context for the model.
//   - PreToolUse refuses on {"decision": "block"}, {"continue": false} or
//     hookSpecificOutput.permissionDecision "deny". "allow", "ask" and
//     updatedInput need changes to the approval flow that are not made yet: they
//     are reported and ignored, never half-applied.
//   - UserPromptSubmit refuses on {"decision": "block"} or {"continue": false}
//     and takes hookSpecificOutput.additionalContext as context.
//   - After a tool has run, or a turn has ended, there is nothing left to refuse;
//     what the script said is reported.
func readVerdict(event, stdout string) verdict {
	text := strings.TrimSpace(stdout)
	if text == "" {
		return verdict{}
	}
	if !strings.HasPrefix(text, "{") {
		if event == UserPromptSubmit {
			return verdict{context: text}
		}
		return verdict{}
	}
	var out scriptOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return verdict{notes: []string{"the script printed something that looks like JSON but cannot be read; ignored"}}
	}

	var v verdict
	if msg := strings.TrimSpace(out.SystemMessage); msg != "" {
		v.notes = append(v.notes, msg)
	}
	reason := firstNonEmpty(out.Reason, out.Specific.PermissionDecisionReason, out.StopReason)
	refuses := strings.EqualFold(out.Decision, "block") || (out.Continue != nil && !*out.Continue)

	switch event {
	case PreToolUse:
		perm := strings.ToLower(strings.TrimSpace(out.Specific.PermissionDecision))
		refuses = refuses || strings.EqualFold(out.Decision, "deny") || perm == "deny"
		if perm == "allow" || perm == "ask" {
			v.notes = append(v.notes, `permissionDecision "`+perm+`" is not supported yet; ignored`)
		}
		if raw := strings.TrimSpace(string(out.Specific.UpdatedInput)); raw != "" && raw != "null" {
			v.notes = append(v.notes, "updatedInput is not supported yet; the tool runs with the arguments the model gave")
		}
	case UserPromptSubmit:
		v.context = strings.TrimSpace(out.Specific.AdditionalContext)
	default:
		if refuses {
			v.notes = append(v.notes, "a refusal after "+event+" cannot change what already happened; recorded only")
		}
		refuses = false
	}
	if refuses {
		v.block, v.reason = true, reason
	}
	return v
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}
