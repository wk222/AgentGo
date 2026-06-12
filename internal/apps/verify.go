package apps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// VerifyResult is returned by verify_inner_app / build_inner_app_iteratively.
type VerifyResult struct {
	Success        bool        `json:"success"`
	AppName        string      `json:"app_name,omitempty"`
	Score          int         `json:"score"`
	MaxScore       int         `json:"max_score"`
	Issues         []FileIssue `json:"issues,omitempty"`
	HasCritical    bool        `json:"has_critical_issues"`
	PingOK         bool        `json:"ping_ok,omitempty"`
	PingError      string      `json:"ping_error,omitempty"`
	FilesChecked   []string    `json:"files_checked,omitempty"`
	AutoFixApplied bool        `json:"auto_fix_applied,omitempty"`
	Message        string      `json:"message,omitempty"`
}

// AppPinger tests runtime reachability (e.g. invoke ping action).
type AppPinger interface {
	PingApp(ctx context.Context, appName string) error
}

// VerifyBundle scans on-disk bundle files and optional ping.
func VerifyBundle(ctx context.Context, appsRoot, appName string, pinger AppPinger) VerifyResult {
	appName = strings.TrimSpace(appName)
	res := VerifyResult{AppName: appName, MaxScore: 100, Score: 100}
	dir := filepath.Join(appsRoot, appName)
	if _, err := os.Stat(dir); err != nil {
		res.Success = false
		res.Score = 0
		res.Issues = []FileIssue{{Severity: "critical", Message: "app directory not found"}}
		res.HasCritical = true
		return res
	}
	checks := []string{AppEntryFile, "static/app.js", "static/style.css", AppMetadataFile}
	for _, rel := range checks {
		full := filepath.Join(dir, rel)
		b, err := os.ReadFile(full)
		if err != nil {
			res.Score -= 25
			res.Issues = append(res.Issues, FileIssue{Severity: "critical", Message: "missing or unreadable: " + rel})
			res.HasCritical = true
			continue
		}
		res.FilesChecked = append(res.FilesChecked, rel)
		for _, iss := range validateAppFile(rel, string(b)) {
			res.Issues = append(res.Issues, iss)
			switch iss.Severity {
			case "critical":
				res.Score -= 30
				res.HasCritical = true
			case "warning":
				res.Score -= 5
			}
		}
	}
	if res.Score < 0 {
		res.Score = 0
	}
	if pinger != nil {
		if err := pinger.PingApp(ctx, appName); err != nil {
			res.PingError = err.Error()
			res.Score -= 10
		} else {
			res.PingOK = true
		}
	}
	res.Success = !res.HasCritical && res.Score >= 70
	if res.Success {
		res.Message = "verification passed"
	} else {
		res.Message = "verification found issues"
	}
	return res
}

// AutoFixBundle applies safe deterministic repairs (helpers injection).
func AutoFixBundle(appsRoot, appName string) (bool, error) {
	dir := filepath.Join(appsRoot, strings.TrimSpace(appName))
	idxPath := filepath.Join(dir, AppEntryFile)
	b, err := os.ReadFile(idxPath)
	if err != nil {
		return false, err
	}
	fixed, content := autoFixIndexHTML(string(b), appName)
	if !fixed {
		return false, nil
	}
	return true, writeTextFile(idxPath, content)
}

func autoFixIndexHTML(content, appName string) (bool, string) {
	lower := strings.ToLower(content)
	if strings.Contains(lower, "agentgo-app-helpers") {
		return false, content
	}
	snippet := `  <script src="/agentgo-app-helpers.js" data-app="` + appName + `"></script>` + "\n"
	if strings.Contains(lower, "</body>") {
		return true, strings.Replace(content, "</body>", snippet+"</body>", 1)
	}
	return true, content + "\n" + snippet
}
