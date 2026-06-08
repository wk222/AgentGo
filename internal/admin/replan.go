package admin

import "strings"

// ReplanAfterFailure appends recovery steps when a step fails (heuristic until LLM replanner).
func ReplanAfterFailure(goal, failedStep, errMsg string) []string {
	base := DefaultPlanSteps(goal)
	recovery := "从失败恢复：" + strings.TrimSpace(failedStep)
	if errMsg != "" {
		if len(errMsg) > 240 {
			errMsg = errMsg[:240] + "…"
		}
		recovery += " — " + errMsg
	}
	return append(base, recovery)
}

// IsRuntimeHealError reports APP_RUNTIME_ERROR style failures.
func IsRuntimeHealError(err error, result string) bool {
	s := strings.ToUpper(errString(err) + " " + result)
	return strings.Contains(s, "APP_RUNTIME_ERROR") ||
		strings.Contains(s, "RUNTIME_ERROR") ||
		strings.Contains(s, "INNER_APP") && strings.Contains(s, "ERROR")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
