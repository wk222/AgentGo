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
	"evidence":                        LayerFoundation, // source snapshots shared by tools and transcript; stdlib only
	"promptbudget":                    LayerFoundation, // shared multilingual prompt estimates; stdlib only
	"db":                              LayerFoundation,
	"sessions":                        LayerFoundation,
	"workspace":                       LayerFoundation,
	"applog":                          LayerFoundation,
	"buildinfo":                       LayerFoundation, // release version stamped at build time + toolchain/OS identity for support; stdlib and x/sys only
	"config":                          LayerFoundation, // typed AGENTGO_* runtime settings (single read point); stdlib only
	"event":                           LayerFoundation,
	"governance":                      LayerSystems,
	"memory":                          LayerSystems,
	"capability":                      LayerSystems,
	"checkpoint":                      LayerSystems,
	"gateway":                         LayerSystems,
	"channels":                        LayerSystems,
	"interactive":                     LayerSystems,
	"externalcontent":                 LayerSystems,
	"sandbox":                         LayerSystems,
	"coderuntime":                     LayerSystems,
	"compose":                         LayerSystems,
	"compose/lifecycle":               LayerSystems,
	"compose/reconciler":              LayerSystems,
	"compose/service":                 LayerSystems,
	"ledger":                          LayerSystems,
	"spill":                           LayerSystems,
	"ideruntime":                      LayerSystems,
	"userhooks":                       LayerSystems,    // scripts the user lists in hooks.json, run around tool calls and messages; stdlib and shellcmd only
	"powernaplsp":                     LayerAssets,     // local LSP transport, used by codelsp
	"processutil":                     LayerFoundation, // platform process flags, stdlib only
	"textenc":                         LayerFoundation, // bytes of a text file -> UTF-8 (BOM, UTF-16, GBK/GB18030); stdlib and x/text only
	"shellcmd":                        LayerFoundation, // runs a command line through the platform shell and ends its process tree; stdlib only
	"plugin":                          LayerFoundation, // plugin host: services, scopes, event middleware; stdlib only
	"hostui":                          LayerFoundation, // browser launch + native folder dialog for the browser build; stdlib only
	"pathgrant":                       LayerFoundation, // folders the user let a run reach outside its workspace; stdlib only
	"linktest":                        LayerFoundation, // test-only: gives tests another spelling of a folder (a link, a Windows 8.3 short name); stdlib only
	"webshell":                        LayerConsumer,   // serves the UI to a browser: HTTP/SSE stand-in for the Wails runtime; stdlib only
	"docindex":                        LayerAssets,     // SQLite full-text index of parsed document chunks behind search_documents
	"browsercontrol":                  LayerAssets,     // drives a local Chrome/Edge over the DevTools protocol (pure Go) for the browser_* tools
	"officedoc":                       LayerAssets,     // native Word/Excel/PowerPoint tools (docx and pptx writers, excelize) for Eino
	"docchunk":                        LayerSystems,    // adaptive document chunking ported from WeKnora; only the bridge "chunker" plugin imports it
	"visionocr":                       LayerAssets,     // read text out of images: vision model on the gateway, or a local engine behind -tags ocr
	"rekit":                           LayerAssets,     // native reverse-engineering toolkit (triage/apk/har/xor/strings/ledger/toolrun)
	"codetools":                       LayerAssets,     // code-aware tools (files, search, LSP) adapted to Eino tools
	"codelsp":                         LayerAssets,     // LSP session supervisor + lsp_* tools on powernap
	"engine":                          LayerSystems,    // unified AgentEngine/EventSink contracts; stdlib only
	"audio":                           LayerSystems,    // kernel side of voice: frames, events, interrupt contract; stdlib only (VAD/ASR/TTS are plugins)
	"tools":                           LayerAssets,
	"skills":                          LayerAssets,
	"workflow":                        LayerAssets,
	"apps":                            LayerAssets,
	"agentpack":                       LayerAssets,
	"kanban":                          LayerAssets,
	"taskhub":                         LayerAssets,
	"scheduler":                       LayerAssets,
	"trigger":                         LayerAssets,
	"threadsub":                       LayerAssets,
	"admin":                           LayerAssets,
	"terminal":                        LayerAssets,
	"evaluation":                      LayerAssets,
	"memeval":                         LayerAssets, // retrieval-eval backend: runs a corpus through the real memory engine; only agentgo-eval and tests import it
	"evolution":                       LayerAssets, // measured harness self-improvement: editable surface + leak guard; stdlib only
	"agent":                           LayerModes,
	"core":                            LayerKernel, // kernel services (chat transcript, ...) shared by gateway/ACP/desktop
	"bridge":                          LayerConsumer,
	"acp":                             LayerConsumer,
	"tui":                             LayerConsumer,
	"cli":                             LayerConsumer,   // AgentGo's own terminal client; drives the brain through bridge.AppService
	"computeruse":                     LayerFoundation, // screen, mouse, keyboard and UI Automation behind the computer_use tool; stdlib, x/sys and processutil only
	"redact":                          LayerFoundation, // hides credentials in text that leaves the process; stdlib only
	"artifact":                        LayerFoundation, // checked, versioned delivery of files the agent made; stdlib only
	"im":                              LayerSystems,    // chat-app bridge: allowlist, sessions, commands, approvals over gateway
	"imchannel":                       LayerFoundation, // chat-app adapters ported from octo-agent (MIT); no agentgo imports outside this tree
	"imchannel/datahome":              LayerFoundation,
	"imchannel/uploads":               LayerFoundation,
	"imchannel/adapters/dingtalk":     LayerFoundation,
	"imchannel/adapters/discord":      LayerFoundation,
	"imchannel/adapters/feishu":       LayerFoundation,
	"imchannel/adapters/telegram":     LayerFoundation,
	"imchannel/adapters/wecom":        LayerFoundation,
	"imchannel/adapters/weixin":       LayerFoundation,
	"imchannel/adapters/weixin/ilink": LayerFoundation,
	"arch":                            LayerFoundation, // meta; only stdlib imports allowed
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
