// Package arch defines AgentGo's formal layer model (PyBot L0–L3 + consumer L4).
// Import rules are enforced by layer_guard_test.go.
package arch

// Layer is a dependency tier; higher numbers may import lower, never the reverse.
type Layer int

const (
	LayerFoundation Layer = 0 // runtime spine: db, sessions, workspace
	LayerSystems    Layer = 1 // governance, memory, capability, gateway, …
	LayerAssets     Layer = 2 // tools, skills, workflow, apps, admin, …
	LayerModes      Layer = 3 // agent orchestration (profiles, matrix, ADK)
	LayerConsumer   Layer = 4 // bridge: Wails IPC + runtime assembly
	LayerCMD        Layer = 5 // cmd/* entrypoints
)

// PackageLayer maps agentgo/internal/<pkg> to its layer.
var PackageLayer = map[string]Layer{
	"db":                 LayerFoundation,
	"sessions":           LayerFoundation,
	"workspace":          LayerFoundation,
	"applog":             LayerFoundation,
	"event":              LayerFoundation,
	"telemetry":          LayerFoundation,
	"governance":         LayerSystems,
	"memory":             LayerSystems,
	"capability":         LayerSystems,
	"checkpoint":         LayerSystems,
	"gateway":            LayerSystems,
	"channels":           LayerSystems,
	"interactive":        LayerSystems,
	"externalcontent":    LayerSystems,
	"sandbox":            LayerSystems,
	"coderuntime":        LayerSystems,
	"compose":            LayerSystems,
	"compose/lifecycle":  LayerSystems,
	"compose/reconciler": LayerSystems,
	"compose/service":    LayerSystems,
	"ledger":             LayerSystems,
	"spill":              LayerSystems,
	"ideruntime":         LayerSystems,
	"hostlink":           LayerFoundation, // host discovery, token auth, loopback guard, attach proxy; stdlib only
	"shellcmd":           LayerFoundation, // runs a command line through the platform shell and ends its process tree; stdlib only
	"plugin":             LayerFoundation, // plugin host: services, scopes, event middleware; stdlib only
	"codetools":          LayerAssets,     // Crush code-aware tools adapted to Eino tools
	"engine":             LayerSystems,    // unified AgentEngine/EventSink contracts; stdlib only
	"engine/crushengine": LayerSystems,    // Crush sidecar engine (HTTP/SSE client; imports engine only)
	"crushproto":         LayerSystems,    // serves Crush's /v1 protocol so the stock Crush TUI can front AgentGo; stdlib only
	"tools":              LayerAssets,
	"skills":             LayerAssets,
	"workflow":           LayerAssets,
	"apps":               LayerAssets,
	"agentpack":          LayerAssets,
	"kanban":             LayerAssets,
	"taskhub":            LayerAssets,
	"scheduler":          LayerAssets,
	"admin":              LayerAssets,
	"terminal":           LayerAssets,
	"evaluation":         LayerAssets,
	"agent":              LayerModes,
	"bridge":             LayerConsumer,
	"acp":                LayerConsumer,
	"tui":                LayerConsumer,
	"arch":               LayerFoundation, // meta; only stdlib imports allowed
}

// ConsumerPackages may import any registered internal package at LayerModes or below.
var ConsumerPackages = map[string]Layer{
	"bridge": LayerConsumer,
}

// CMDPackages are entrypoints; may only import bridge (+ stdlib).
var CMDPackages = map[string]bool{
	"agentgo/cmd/agentgo": true,
	"agentgo/cmd/llmtest": true,
}

// LayerLabel returns a human-readable name.
func LayerLabel(l Layer) string {
	switch l {
	case LayerFoundation:
		return "L0 Foundation"
	case LayerSystems:
		return "L1 Systems"
	case LayerAssets:
		return "L2 Assets"
	case LayerModes:
		return "L3 Modes"
	case LayerConsumer:
		return "L4 Consumer (bridge)"
	case LayerCMD:
		return "L5 CMD"
	default:
		return "unknown"
	}
}
