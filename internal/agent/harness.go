package agent

import (
	"strings"

	"agentgo/internal/config"
)

// Harness is the user-facing working mode: one of a few named presets that the UI, the
// ACP editor bridge and the gateway all expose. It is a thin, named view over the
// internal SessionMode (Profile x Canvas); Profile/Canvas stay the storage and policy
// representation, Harness is the vocabulary users and clients see. Define a new preset
// here and every client picks it up through ListHarnesses.
type Harness struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`  // short Chinese name
	Label   string          `json:"label"` // "<name> <English>" for compact displays
	Desc    string          `json:"desc"`
	Profile ModeProfile     `json:"profile"`
	Canvas  ExecutionCanvas `json:"canvas"`
}

// Harness ids.
const (
	HarnessMinimal    = "minimal"
	HarnessStandard   = "standard"
	HarnessUltimate   = "ultimate"
	HarnessCreative   = "creative"
	HarnessReverse    = "reverse"
	HarnessArmorBreak = "armor_break"
)

// DefaultHarnessID is what a session without an explicit choice runs as.
const DefaultHarnessID = HarnessStandard

var harnesses = []Harness{
	{HarnessMinimal, "极简", "极简 Minimal", "目标明确的问答与单步任务，直接动手", ModeAssistant, CanvasFocused},
	{HarnessStandard, "标准", "标准 Standard", "日常多步工程任务：规划 → 执行 → 验证", ModeAssistant, CanvasBalanced},
	{HarnessUltimate, "极致", "极致 Ultimate", "复杂任务深度拆解、全量工具协同与闭环自检", ModeAdmin, CanvasDeep},
	{HarnessCreative, "创造", "创造 Creative", "构建、预览与迭代 Inner App 及工作流", ModeAppMatrix, CanvasBalanced},
	{HarnessReverse, "逆向", "逆向 Reverse", "证据分级（观察/推导/推断/未知）、静态优先探查与门控校验；默认开启破甲（自动批准；模型拒答且没有工具输出时，先问是否越狱，同意后才回退续跑）", ModeReverse, CanvasBalanced},
}

var harnessArmorBreak = Harness{
	HarnessArmorBreak, "破甲", "破甲 ArmorBreak", "本地红队实测：开放工具、自动批准、弱化模型拒答提示（需 AGENTGO_ARMOR_BREAK=1）", ModeRedTeam, CanvasDeep,
}

func allHarnesses() []Harness {
	out := append([]Harness(nil), harnesses...)
	if config.ArmorBreak.On() {
		out = append(out, harnessArmorBreak)
	}
	return out
}

// Harnesses returns the presets in display order (a copy; callers may modify it).
func Harnesses() []Harness { return allHarnesses() }

// HarnessByID looks a preset up by id, case-insensitively.
func HarnessByID(id string) (Harness, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, h := range allHarnesses() {
		if h.ID == id {
			return h, true
		}
	}
	return Harness{}, false
}

// legacyHarnessAliases maps ids older clients still send (the editor bridge used
// architect/coder/ask) onto presets.
var legacyHarnessAliases = map[string]string{
	"architect":           HarnessUltimate,
	"coder":               HarnessStandard,
	"ask":                 HarnessMinimal,
	"re":                  HarnessReverse,
	"reverse-engineering": HarnessReverse,
}

// ResolveHarness is HarnessByID that also understands legacy aliases.
func ResolveHarness(id string) (Harness, bool) {
	if h, ok := HarnessByID(id); ok {
		return h, true
	}
	if target, ok := legacyHarnessAliases[strings.ToLower(strings.TrimSpace(id))]; ok {
		return HarnessByID(target)
	}
	return Harness{}, false
}

// Mode is the SessionMode this preset runs as.
func (h Harness) Mode() SessionMode { return SessionMode{Profile: h.Profile, Canvas: h.Canvas} }

// Harness returns the preset that best describes m. Presets map exactly; any other
// Profile x Canvas combination (for example one stored by an older version or set via
// environment variables) falls back to the nearest preset: an app_matrix profile reads
// as creative, an admin profile or deep canvas as ultimate, a focused canvas as minimal.
func (m SessionMode) Harness() Harness {
	p, c := m.normalized()
	var id string
	switch {
	case p == ModeRedTeam && config.ArmorBreak.On():
		id = HarnessArmorBreak
	case p == ModeReverse:
		id = HarnessReverse
	case p == ModeAppMatrix:
		id = HarnessCreative
	case p == ModeAdmin || c == CanvasDeep:
		id = HarnessUltimate
	case c == CanvasFocused:
		id = HarnessMinimal
	default:
		id = HarnessStandard
	}
	h, _ := HarnessByID(id)
	return h
}
