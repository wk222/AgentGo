package acpclient

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Preset is a coding agent known to speak ACP, with the command line that starts it.
type Preset struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	Binary  string `json:"binary"` // the program that must be on PATH for Command to work
	Note    string `json:"note,omitempty"`
}

// presets are agents that speak ACP on stdio when started this way. The npx ones are bridges the
// vendors publish; the first run downloads them, which can take a minute.
var presets = []Preset{
	{ID: "claude", Command: "npx -y @agentclientprotocol/claude-agent-acp", Binary: "npx", Note: "Claude Code (needs its login or ANTHROPIC_API_KEY)"},
	{ID: "codex", Command: "npx -y @zed-industries/codex-acp", Binary: "npx", Note: "OpenAI Codex (needs its login or OPENAI_API_KEY)"},
	{ID: "gemini", Command: "gemini --acp", Binary: "gemini", Note: "Gemini CLI"},
	{ID: "qwen", Command: "qwen --acp", Binary: "qwen", Note: "Qwen Code"},
	{ID: "opencode", Command: "opencode acp", Binary: "opencode", Note: "OpenCode"},
	{ID: "hermes", Command: "hermes acp", Binary: "hermes", Note: "Hermes Agent"},
	{ID: "openclaw", Command: "openclaw acp", Binary: "openclaw", Note: "OpenClaw"},
	{ID: "codebuddy", Command: "npx -y --package @tencent-ai/codebuddy-code codebuddy --acp", Binary: "npx", Note: "CodeBuddy"},
}

// Agents lists the built-in presets plus the ones the user configured, which win on a name clash.
func Agents(custom map[string]string) []Preset {
	out := append([]Preset(nil), presets...)
	for name, line := range custom {
		name, line = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(line)
		if name == "" || line == "" {
			continue
		}
		p := Preset{ID: name, Command: line, Binary: firstWord(line), Note: "configured"}
		replaced := false
		for i := range out {
			if out[i].ID == name {
				out[i], replaced = p, true
			}
		}
		if !replaced {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Lookup finds an agent by name.
func Lookup(name string, custom map[string]string) (Preset, error) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, p := range Agents(custom) {
		if p.ID == want {
			return p, nil
		}
	}
	ids := make([]string, 0, 8)
	for _, p := range Agents(custom) {
		ids = append(ids, p.ID)
	}
	return Preset{}, fmt.Errorf("unknown agent %q (known: %s)", name, strings.Join(ids, ", "))
}

// Installed reports whether the program a preset needs can be found.
func (p Preset) Installed() bool {
	_, err := exec.LookPath(p.Binary)
	return err == nil
}

func firstWord(line string) string {
	f := strings.Fields(line)
	if len(f) == 0 {
		return ""
	}
	return strings.Trim(f[0], `"'`)
}

// ParseCustom reads a JSON object of name → command line, the format of the setting that adds
// agents. An empty string is no agents; malformed JSON is an error so a typo is not silent.
func ParseCustom(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("the extra-agents setting must be a JSON object like {\"mine\":\"my-agent --acp\"}: %w", err)
	}
	return m, nil
}
