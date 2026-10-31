//go:build web

package bridge

import (
	"net/url"
	"strings"

	"agentgo/internal/hostui"
)

// hostKind names the shell this binary is: the browser build.
const hostKind = "web"

// desktopApp has no meaning without a Wails window; the field stays nil.
type desktopApp = *struct{}

func desktopEventsReady() bool { return false }

func desktopEmit(string, any) {}

// ForwardFileDrops is a no-op here: the browser build has no native window (the
// page works the path out itself, see ResolveDroppedFile). It takes any so the
// shared desktop entry point still compiles under -tags web without Wails types.
func ForwardFileDrops(any) {}

// openExternalURL opens a link in a browser on the machine running AgentGo.
func (s *AppService) openExternalURL(link string) error {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return err
	}
	return hostui.FindBrowser().Open(u.String())
}

// openUtilityWindow asks the page to open a new browser tab: a browser build has
// no separate native windows. The web shell script reads "open_url".
func (s *AppService) openUtilityWindow(name, title, windowURL string, width, height, minWidth, minHeight int) map[string]any {
	return map[string]any{"success": true, "window": name, "open_url": windowURL}
}
