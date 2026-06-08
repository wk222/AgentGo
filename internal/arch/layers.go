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
	"db":              LayerFoundation,
	"sessions":        LayerFoundation,
	"workspace":       LayerFoundation,
	"applog":          LayerFoundation,
	"event":           LayerFoundation,
	"telemetry":       LayerFoundation,
	"governance":      LayerSystems,
	"memory":          LayerSystems,
	"capability":      LayerSystems,
	"checkpoint":      LayerSystems,
	"gateway":         LayerSystems,
	"channels":        LayerSystems,
	"interactive":     LayerSystems,
	"externalcontent": LayerSystems,
	"sandbox":         LayerSystems,
	"tools":           LayerAssets,
	"skills":          LayerAssets,
	"workflow":        LayerAssets,
	"apps":            LayerAssets,
	"agentpack":       LayerAssets,
	"kanban":          LayerAssets,
	"taskhub":         LayerAssets,
	"scheduler":       LayerAssets,
	"admin":           LayerAssets,
	"terminal":        LayerAssets,
	"evaluation":      LayerAssets,
	"agent":           LayerModes,
	"bridge":          LayerConsumer,
	"arch":            LayerFoundation, // meta; only stdlib imports allowed
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
