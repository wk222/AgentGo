// Package codelsp is AgentGo's language-server layer for the code tools. It
// decides which servers to run for a workspace, keeps one session per server
// with a cache of the diagnostics it publishes, and offers the lsp_* tools on
// top. The wire protocol and the bundled server definitions come from the
// standalone powernap library (github.com/charmbracelet/x/powernap, MIT).
package codelsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	powernap "agentgo/internal/powernaplsp"
	powernapconfig "github.com/charmbracelet/x/powernap/pkg/config"
)

// ServerSpec says how to start one language server and which files it serves.
type ServerSpec struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	FileTypes   []string          `json:"filetypes,omitempty"`
	RootMarkers []string          `json:"root_markers,omitempty"`
	InitOptions map[string]any    `json:"init_options,omitempty"`
	Settings    map[string]any    `json:"options,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
	// TimeoutSec bounds the initialize handshake; the default is 30.
	TimeoutSec int `json:"timeout,omitempty"`
}

// Overrides are the user's server settings, keyed by server name. A key that
// matches a bundled server replaces its definition; a new key adds a server;
// Disabled removes one. User-configured servers skip the checks that guard the
// bundled ones (PATH lookup, launcher denylist).
type Overrides map[string]ServerSpec

// LoadOverrides reads <dir>/lsp.json, a JSON object of name -> ServerSpec. A
// missing file is not an error; a malformed one is.
func LoadOverrides(dir string) (Overrides, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "lsp.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var o Overrides
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	return o, nil
}

// launcherCommands are generic interpreters and build tools. A bundled server
// whose command is one of them is never started on its own: running
// `python` or `java` merely because a file of that language was touched would
// be surprising. The user can still configure such a server explicitly.
var launcherCommands = map[string]bool{}

func init() {
	for _, c := range []string{
		"node", "npx", "deno", "dotnet", "java", "python", "python3", "perl", "julia", "racket", "R", "swipl",
		"buck2", "plz", "buf", "cue", "dart", "gleam", "koka", "ruff", "rubocop", "rome", "scarb", "solc",
		"dprint", "stylua", "tflint",
	} {
		launcherCommands[c] = true
	}
}

// catalog is the table of servers a workspace may use.
type catalog struct {
	specs map[string]ServerSpec
	user  map[string]bool // names configured by the user
}

// buildCatalog starts from powernap's bundled definitions and applies the
// user's overrides on top.
func buildCatalog(o Overrides) *catalog {
	cat := &catalog{specs: map[string]ServerSpec{}, user: map[string]bool{}}
	bundled := powernapconfig.NewManager()
	bundled.LoadDefaults()
	for name, s := range bundled.GetServers() {
		cat.specs[name] = ServerSpec{
			Command: s.Command, Args: s.Args, Env: s.Environment, FileTypes: s.FileTypes,
			RootMarkers: s.RootMarkers, InitOptions: s.InitOptions, Settings: s.Settings,
		}
	}
	for name, spec := range o {
		target := cat.resolveName(name)
		if spec.Disabled {
			delete(cat.specs, target)
			continue
		}
		cat.specs[target] = spec
		cat.user[target] = true
	}
	return cat
}

// resolveName maps a key from lsp.json to a catalogue entry. People sometimes
// write the command ("typescript-language-server") where the name belongs, so a
// key that names no server but equals some server's command selects that one.
func (c *catalog) resolveName(key string) string {
	if _, known := c.specs[key]; known {
		return key
	}
	for name, s := range c.specs {
		if s.Command == key {
			return name
		}
	}
	return key
}

// serves reports whether spec applies to path in a workspace rooted at root.
func (s ServerSpec) serves(path, root string) bool {
	return matchesFileType(s.FileTypes, path) && hasRootMarker(root, s.RootMarkers)
}

// matchesFileType is true when path has one of the extensions (or language ids)
// in types. An empty list means every file.
func matchesFileType(types []string, path string) bool {
	if len(types) == 0 {
		return true
	}
	lang := string(powernap.DetectLanguage(path))
	base := strings.ToLower(filepath.Base(path))
	for _, t := range types {
		ext := strings.ToLower(t)
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		if strings.HasSuffix(base, ext) || t == lang {
			return true
		}
	}
	return false
}

// hasRootMarker looks for any marker (a file name or glob) directly in root. It
// does not search subfolders: walking a monorepo for markers costs far more than
// it is worth.
func hasRootMarker(root string, markers []string) bool {
	if len(markers) == 0 {
		return true
	}
	for _, m := range markers {
		if found, err := filepath.Glob(filepath.Join(root, m)); err == nil && len(found) > 0 {
			return true
		}
	}
	return false
}

// within reports whether path is root or lies below it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
