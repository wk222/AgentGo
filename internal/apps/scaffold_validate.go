package apps

import (
	"strings"
)

// FileIssue is a validation finding for update_inner_app_file.
type FileIssue struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

func validateAppFile(relPath, content string) []FileIssue {
	rel := strings.ReplaceAll(relPath, `\`, "/")
	switch rel {
	case AppEntryFile:
		return validateIndexHTML(content)
	case "static/app.js":
		return validateAppJS(content)
	default:
		return nil
	}
}

func validateIndexHTML(content string) []FileIssue {
	var issues []FileIssue
	lower := strings.ToLower(content)
	if !strings.Contains(lower, "agentgo-app-helpers") && !strings.Contains(lower, "agentgo.apiCall") {
		issues = append(issues, FileIssue{
			Severity: "critical",
			Message:  "index.html 应包含 /agentgo-app-helpers.js（在 static/app.js 之前）",
		})
	}
	if strings.Contains(content, "static/app.js") && strings.Index(lower, "app.js") < strings.Index(lower, "agentgo-app-helpers") {
		if strings.Contains(lower, "static/app.js") {
			idxHelpers := strings.Index(lower, "agentgo-app-helpers")
			idxApp := strings.Index(lower, "static/app.js")
			if idxHelpers < 0 || (idxApp >= 0 && idxApp < idxHelpers) {
				issues = append(issues, FileIssue{
					Severity: "warning",
					Message:  "建议先加载 agentgo-app-helpers.js，再加载 static/app.js",
				})
			}
		}
	}
	return issues
}

func validateAppJS(content string) []FileIssue {
	if strings.TrimSpace(content) == "" {
		return []FileIssue{{Severity: "warning", Message: "app.js 为空"}}
	}
	return nil
}
