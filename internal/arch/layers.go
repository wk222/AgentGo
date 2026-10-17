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
	LayerKernel     Layer = 4 // core: business logic shared by every entry point
	LayerConsumer   Layer = 5 // bridge / acp / cli: protocol adapters + runtime assembly
	LayerCMD        Layer = 6 // cmd/* entrypoints
)

// PackageLayer maps agentgo/internal/<pkg> to its layer.
var PackageLayer = map[string]Layer{
	"db":                 LayerFoundation,
	"sessions":           LayerFoundation,
	"workspace":          LayerFoundation,
	"applog":             LayerFoundation,
	"config":             LayerFoundation, // typed AGENTGO_* runtime settings (single read point); stdlib only
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
	"shellcmd":           LayerFoundation, // runs a command line through the platform shell and ends its process tree; stdlib only
	"plugin":             LayerFoundation, // plugin host: services, scopes, event middleware; stdlib only
	"officedoc":          LayerAssets,     // native Word/Excel generation (docx writer, excelize) as Eino tools
	"codetools":          LayerAssets,     // Crush code-aware tools adapted to Eino tools
	"codelsp":            LayerAssets,     // native LSP client manager + lsp_* tools on powernap (replaces Crush's lsp package)
	"engine":             LayerSystems,    // unified AgentEngine/EventSink contracts; stdlib only
	"audio":              LayerSystems,    // kernel side of voice: frames, events, interrupt contract; stdlib only (VAD/ASR/TTS are plugins)
	"tools":              LayerAssets,
	"skills":             LayerAssets,
	"workflow":           LayerAssets,
	"apps":               LayerAssets,
	"agentpack":          LayerAssets,
	"kanban":             LayerAssets,
	"taskhub":            LayerAssets,
	"scheduler":          LayerAssets,
	"trigger":            LayerAssets,
	"admin":              LayerAssets,
	"terminal":           LayerAssets,
	"evaluation":         LayerAssets,
	"agent":              LayerModes,
	"core":               LayerKernel,     // kernel services (chat transcript, ...) shared by gateway/ACP/desktop
	"bridge":             LayerConsumer,
	"acp":                LayerConsumer,
	"tui":                LayerConsumer,
	"cli":                LayerConsumer,   // AgentGo's own terminal client; drives the brain through bridge.AppService
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
	case LayerKernel:
		return "L4 Kernel (core)"
	case LayerConsumer:
		return "L5 Consumer (bridge)"
	case LayerCMD:
		return "L6 CMD"
	default:
		return "unknown"
	}
}
