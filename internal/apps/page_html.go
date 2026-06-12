package apps

import (
	"context"
	"fmt"
)

// PageHTML loads index.html and ensures agentgo-app-helpers is present.
func PageHTML(ctx context.Context, store *Store, appsRoot, workspace, appName string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("app store unavailable")
	}
	app, err := store.GetByName(ctx, appName)
	if err != nil {
		return "", err
	}
	dir, ok := ResolveBundleDir(appsRoot, workspace, app)
	if !ok {
		return "", fmt.Errorf("bundle not found for app %q", appName)
	}
	b, _, err := ReadBundleFile(dir, AppEntryFile)
	if err != nil {
		return "", err
	}
	return InjectAppHelpers(string(b), appName), nil
}

// InjectAppHelpers inserts the helpers script before </head> when missing.
func InjectAppHelpers(html, appName string) string {
	if containsFold(html, "agentgo-app-helpers") {
		return html
	}
	helper := `<script src="/agentgo-app-helpers.js" data-app="` + appName + `"></script>`
	if i := indexFold(html, "</head>"); i >= 0 {
		return html[:i] + helper + html[i:]
	}
	return helper + html
}

func containsFold(s, sub string) bool {
	return indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	ls, lsub := len(s), len(sub)
	if lsub == 0 {
		return 0
	}
	for i := 0; i+lsub <= ls; i++ {
		if equalFoldAt(s, i, sub) {
			return i
		}
	}
	return -1
}

func equalFoldAt(s string, i int, sub string) bool {
	for j := 0; j < len(sub); j++ {
		a, b := s[i+j], sub[j]
		if a == b {
			continue
		}
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}
