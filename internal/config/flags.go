package config

// The complete list of AGENTGO_* settings. Add a flag here first, then read it
// through its handle; there is no other way to read one.

// --- model / LLM -----------------------------------------------------------

var (
	APIKey = newSecret("AGENTGO_API_KEY", GroupModel, "LLM API key (overrides config.json when set)")
	Model  = newStr("AGENTGO_MODEL", "gpt-4o-mini", GroupModel, "default chat model name")

	AzureDeployment = newStr("AGENTGO_AZURE_DEPLOYMENT", "", GroupModel,
		"Azure deployment name used for the Luna model path")
	UseLuna = newBool("AGENTGO_USE_LUNA", false, GroupModel, "start with the Luna model preset (same as --luna)")

	LLMAPI = newStr("AGENTGO_LLM_API", "", GroupModel,
		"model channel: empty = chat completions, \"responses\" = OpenAI Responses API")
	ReasoningEffort = newStr("AGENTGO_REASONING_EFFORT", "", GroupModel,
		"reasoning_effort sent to the model (none|low|medium|high); empty = model default")
	ReasoningSummary = newStr("AGENTGO_REASONING_SUMMARY", "", GroupModel,
		"Responses API reasoning summary mode (auto|concise|detailed)")
	SystemPrompt = newStr("AGENTGO_SYSTEM_PROMPT", "", GroupModel, "replaces the built-in persona prompt")
	RewriteModel = newStr("AGENTGO_REWRITE_MODEL", "", GroupModel, "model used for recall query rewriting")
)

// --- agent pipeline --------------------------------------------------------

var (
	ModeProfile = newStr("AGENTGO_MODE_PROFILE", "", GroupAgent,
		"startup session profile (assistant|app_matrix|reverse|admin)")
	ExecutionCanvas = newStr("AGENTGO_EXECUTION_CANVAS", "", GroupAgent,
		"startup execution canvas (focused|balanced|deep)")
	WorkspaceRoot = newStr("AGENTGO_WORKSPACE_ROOT", "", GroupAgent,
		"workspace directory the agent works in (overrides the remembered one)")
	Workspace = newStr("AGENTGO_WORKSPACE", "", GroupAgent, "workspace used by memory journaling hooks")

	DisableAgentsMD = newBool("AGENTGO_DISABLE_AGENTSMD", false, GroupAgent, "do not inject AGENTS.md files")
	AgentsMDParents = newBool("AGENTGO_AGENTSMD_PARENTS", true, GroupAgent,
		"also load AGENTS.md (or CLAUDE.md) of the parent directories up to the git root, and the user-level AGENTS.md in the data directory")
	EinoSkillMW    = newBool("AGENTGO_EINO_SKILL_MW", false, GroupAgent, "mount the Eino skill middleware")
	EinoPlanTaskMW = newBool("AGENTGO_EINO_PLANTASK", false, GroupAgent, "legacy alias gate for the plan-task middleware")
	EinoBGTask     = newBool("AGENTGO_EINO_BG_TASK", true, GroupAgent, "background task control middleware")
	EinoPlanTask   = newBool("AGENTGO_EINO_PLAN_TASK", true, GroupAgent, "plan task middleware (TaskCreate/Update/...)")
	EinoSpill      = newBool("AGENTGO_EINO_SPILL", true, GroupAgent, "offload large tool outputs to the spill store")
	TurnLoop       = newBool("AGENTGO_TURN_LOOP", false, GroupAgent, "run chats through the ADK TurnLoop")
	ToolHeal       = newBool("AGENTGO_TOOL_HEAL", true, GroupAgent, "repair malformed tool-call JSON and retry")
	ToolBarrier    = newBool("AGENTGO_TOOL_BARRIER", true, GroupAgent, "run a reply's file-changing and undeclared tool calls in the order listed; read-only tools stay parallel")

	ReductionTrunc = newBool("AGENTGO_REDUCTION_TRUNC", false, GroupAgent,
		"enable truncation in the reduction middleware (clearing is always on)")
	ReductionBackend = newStr("AGENTGO_REDUCTION_BACKEND", "", GroupAgent,
		"directory for reduction offload files; empty = under the data dir")
	EpisodicCompress = newBool("AGENTGO_EPISODIC_COMPRESS", false, GroupAgent,
		"1 = always compress episodic history, 0 = never; unset = automatic by turn count")

	AdminLegacy      = newBool("AGENTGO_ADMIN_LEGACY", false, GroupAgent, "admin tasks use the legacy Generate path")
	AdminPlanExecute = newBool("AGENTGO_ADMIN_PLANEXECUTE", false, GroupAgent, "admin tasks use plan-execute instead of deep")
	AdminPEOnly      = newBool("AGENTGO_ADMIN_PE_ONLY", false, GroupAgent, "plan-execute failures are errors (no fallback)")
	AdminDeepOnly    = newBool("AGENTGO_ADMIN_DEEP_ONLY", false, GroupAgent, "deep-agent failures are errors (no fallback)")
)

// --- memory ----------------------------------------------------------------

// Exact hosts approved by the operator for private-network page reads. These
// are configuration, never model-supplied tool arguments.
var WebFetchPrivateHosts = newStr("AGENTGO_WEB_FETCH_PRIVATE_HOSTS", "", GroupPlugins,
	"comma-separated exact hosts allowed for private-network web_fetch (no wildcards); public HTTP(S) hosts work by default")

// Browser takeover: the agent drives a real Chrome/Edge window over the DevTools
// protocol. Like the web_fetch allowance, these are operator configuration and
// never tool arguments.
var (
	BrowserControl = newBool("AGENTGO_BROWSER_CONTROL", true, GroupPlugins,
		"let the agent drive a Chrome/Edge window (browser_* tools, every acting step needs approval); only registered when a browser is found")
	BrowserPath = newStr("AGENTGO_BROWSER_PATH", "", GroupPlugins,
		"full path of the Chrome/Edge executable to drive; empty = look in the usual install locations")
	BrowserPrivateHosts = newStr("AGENTGO_BROWSER_PRIVATE_HOSTS", "", GroupPlugins,
		"comma-separated exact hosts (or IPs) the browser tools may open on private networks, e.g. an intranet OA system (no wildcards); public sites work by default")
	BrowserHeadless = newBool("AGENTGO_BROWSER_HEADLESS", false, GroupPlugins,
		"start the driven browser without a visible window")
)

var (
	AutoLearn    = newBool("AGENTGO_AUTO_LEARN", true, GroupMemory, "extract evidence-linked reusable memories in a durable background queue; never trains model weights")
	AutoReflect  = newBool("AGENTGO_AUTO_REFLECT", false, GroupMemory, "reflect on each turn after it finishes")
	AutoGC       = newBool("AGENTGO_AUTO_GC", false, GroupMemory, "garbage-collect memory after turns")
	MemoryGarden = newBool("AGENTGO_MEMORY_GARDEN", false, GroupMemory, "run the memory garden after turns")
	AutoJournal  = newBool("AGENTGO_AUTO_JOURNAL", false, GroupMemory, "write a diary entry after turns")

	EmbeddingModel = newStr("AGENTGO_EMBEDDING_MODEL", "text-embedding-3-small", GroupMemory, "embedding model name")
	EmbeddingDim   = newInt("AGENTGO_EMBEDDING_DIM", 1536, GroupMemory, "embedding vector dimension")
	MilvusAddr     = newStr("AGENTGO_MILVUS_ADDR", "", GroupMemory, "Milvus address; empty = vector store disabled")
	MilvusColl     = newStr("AGENTGO_MILVUS_COLLECTION", "agentgo_memory", GroupMemory, "Milvus collection name")

	MemoryTokens      = newInt("AGENTGO_MEMORY_TOKENS", 4000, GroupMemory, "resident model-visible memory token budget, including summary tree and progressive reads (0 disables)")
	MemoryAutoTokens  = newInt("AGENTGO_MEMORY_AUTO_TOKENS", 1000, GroupMemory, "automatic memory injection budget within MEMORY_TOKENS")
	MemoryHierarchy   = newBool("AGENTGO_MEMORY_HIERARCHY", true, GroupMemory, "browse memory by topic, kind and original source")
	RecallTimeoutMS   = newInt("AGENTGO_RECALL_TIMEOUT_MS", 800, GroupMemory, "recall budget per query (ms)")
	RecallFTSWeight   = newFloat("AGENTGO_RECALL_FTS_WEIGHT", 0.45, GroupMemory, "full-text weight in hybrid recall")
	RecallMilvusW     = newFloat("AGENTGO_RECALL_MILVUS_WEIGHT", 0.55, GroupMemory, "vector weight in hybrid recall")
	RecallEmbedRerank = newBool("AGENTGO_RECALL_EMBED_RERANK", false, GroupMemory, "rerank recall results by embedding")
)

// --- app matrix ------------------------------------------------------------

var (
	MatrixAutoFollowup = newBool("AGENTGO_MATRIX_AUTO_FOLLOWUP", false, GroupMatrix,
		"master switch for capability-bus follow-up orchestration")
	MatrixSupervisor = newBool("AGENTGO_MATRIX_SUPERVISOR", false, GroupMatrix,
		"supervisor tier; unset follows MATRIX_AUTO_FOLLOWUP")
	MatrixCompose = newBool("AGENTGO_MATRIX_COMPOSE", false, GroupMatrix,
		"compose tier; unset follows MATRIX_AUTO_FOLLOWUP")
	MatrixEmitEvents = newBool("AGENTGO_MATRIX_EMIT_EVENTS", false, GroupMatrix,
		"emit sub-agent internal events; unset follows MATRIX_AUTO_FOLLOWUP")
)

// --- gateway / hosts -------------------------------------------------------

var (
	GatewayAddr  = newStr("AGENTGO_GATEWAY_ADDR", "", GroupGateway, "HTTP gateway listen address (never beyond loopback unless set)")
	GatewayPort  = newStr("AGENTGO_GATEWAY_PORT", "", GroupGateway, "HTTP gateway port on loopback")
	GatewayToken = newSecret("AGENTGO_GATEWAY_TOKEN", GroupGateway, "gateway bearer token; generated when empty")
)

// --- plugins / background services ----------------------------------------

var (
	PluginsDisable = newStr("AGENTGO_PLUGINS_DISABLE", "", GroupPlugins, "comma-separated plugin names to switch off")
	CodeTools      = newBool("AGENTGO_CODETOOLS", true, GroupPlugins, "code-aware tools plugin")
	CodeToolsAppr  = newBool("AGENTGO_CODETOOLS_APPROVE", true, GroupPlugins, "0 denies every mutating code tool (read-only)")
	OfficeDoc      = newBool("AGENTGO_OFFICEDOC", true, GroupPlugins, "Word/Excel generation plugin")
	DocIndex       = newBool("AGENTGO_DOC_INDEX", true, GroupPlugins, "searchable document index: index_documents / search_documents over chosen PDF, DOCX, XLSX and text files, with page and row citations")
	ReKit          = newBool("AGENTGO_REKIT", true, GroupPlugins, "reverse-engineering toolkit plugin (re_* tools)")
	UserHooks      = newBool("AGENTGO_USER_HOOKS", true, GroupPlugins, "run the scripts listed in <data dir>/hooks.json around tool calls (0 switches them all off)")
	ReHome         = newStr("AGENTGO_RE_HOME", "", GroupPlugins, "case/ledger/spill directory for rekit (default <workspace>/.agentgo-re)")
	ReAllowDynamic = newBool("AGENTGO_RE_ALLOW_DYNAMIC", false, GroupPlugins, "allow dynamic reverse tools (adb, frida) in rekit")
	LegacyDynExec  = newBool("AGENTGO_LEGACY_DYNAMIC_EXEC", false, GroupPlugins, "use the legacy dynamic tool executor")

	DisableScheduler = newBool("AGENTGO_DISABLE_SCHEDULER", false, GroupPlugins, "do not start the cron scheduler (headless hosts)")
	DisableTriggers  = newBool("AGENTGO_DISABLE_TRIGGERS", false, GroupPlugins, "do not start trigger watchers")
	DisableIM        = newBool("AGENTGO_DISABLE_IM", false, GroupPlugins, "do not connect IM bots from channels.yml (only one process may poll the same bot)")

	MCPCommand = newStr("AGENTGO_MCP_COMMAND", "", GroupPlugins, "start one MCP server with this command at boot")
	MCPArgs    = newStr("AGENTGO_MCP_ARGS", "", GroupPlugins, "space-separated arguments for MCP_COMMAND")

	// Computer use hands the model the real mouse and keyboard, so it is opt-in.
	ComputerUse = newBool("AGENTGO_COMPUTER_USE", false, GroupPlugins,
		"register the computer_use tool: the model sees the screen and drives the mouse and keyboard (Windows; Ctrl+Alt+Esc stops it)")
	ComputerUseDir = newStr("AGENTGO_COMPUTER_USE_DIR", "", GroupPlugins,
		"folder for the last few computer_use screenshots (default: a folder under the system temp dir)")
	ComputerUseSidecar = newStr("AGENTGO_COMPUTER_USE_SIDECAR", "", GroupPlugins,
		"path to agentgo-cua.exe: run computer use in that process over a named pipe (e.g. started elevated) instead of in-process")

	// Document chunking. The adaptive splitter is ported from WeKnora. It is
	// compiled into every build, including the Windows 7 one, and is off by
	// default: the built-in splitter stays in charge until a recall comparison
	// against docs/eval-baseline.md says otherwise.
	DocChunk = newBool("AGENTGO_DOCCHUNK", false,
		GroupPlugins, "split long memories with the adaptive document chunker (512/80 + heading breadcrumbs) instead of the built-in 1200/200 paragraph splitter")
	DocChunkStrategy = newStr("AGENTGO_DOCCHUNK_STRATEGY", "", GroupPlugins,
		"chunking tier: auto (default), heading, heuristic, recursive or legacy")

	// OCR. The default provider is a vision model on the gateway, so this works
	// without cgo or local model files (and therefore on Win7).
	OCR = newBool("AGENTGO_OCR", false, GroupPlugins,
		"mount the ocr_image tool so the agent can read text out of images")
	OCRProvider = newStr("AGENTGO_OCR_PROVIDER", "", GroupPlugins,
		"OCR backend: vlm (vision model on the gateway); onnx is reserved for a local engine that is not built yet; empty picks the first available")
	OCRVisionModel = newStr("AGENTGO_OCR_VLM_MODEL", "", GroupModel,
		"model that reads images for ocr_image; empty uses AGENTGO_VISION_MODEL, then the chat model")
	VisionModel = newStr("AGENTGO_VISION_MODEL", "", GroupModel,
		"model used for image understanding; empty uses the chat model")
	OCRTimeoutSec = newInt("AGENTGO_OCR_TIMEOUT_SEC", 120, GroupPlugins,
		"time limit of one OCR call in seconds")
)

// --- sandbox / workflow ----------------------------------------------------

var (
	SandboxMode        = newStr("AGENTGO_SANDBOX_MODE", "", GroupSandbox, "execution seam: local (default) or docker")
	SandboxDocker      = newBool("AGENTGO_SANDBOX_DOCKER", false, GroupSandbox, "run bash/python tools inside Docker")
	SandboxImageBash   = newStr("AGENTGO_SANDBOX_IMAGE_BASH", "node:20-alpine", GroupSandbox, "Docker image for bash tools")
	SandboxImagePython = newStr("AGENTGO_SANDBOX_IMAGE_PYTHON", "", GroupSandbox, "Docker image for python tools")

	// Safety nets under the native write/edit tools.
	AllowSecretWrites = newBool("AGENTGO_ALLOW_SECRET_WRITES", false, GroupSandbox,
		"let write/edit put credential-looking text into files (off: such writes are refused)")
	OverwriteBackup = newBool("AGENTGO_OVERWRITE_BACKUP", true, GroupSandbox,
		"copy a file's previous content to <workspace>/.agentgo/overwrite-backups before write/edit replaces it")
	TurnCheckpoints = newBool("AGENTGO_CHECKPOINTS", true, GroupSandbox,
		"keep the workspace's files as they were before each turn, in memory (lost on restart), so a turn can be rolled back; files over 8 MB and folders such as .git and node_modules are not covered")

	// OS-level confinement of execute_bash (Linux Landlock+seccomp, macOS Seatbelt,
	// Windows Low-integrity token).
	SandboxOS = newBool("AGENTGO_SANDBOX_OS", false, GroupSandbox,
		"confine execute_bash with the OS sandbox so a command can write only in the workspace and a private temp folder; fails closed where unsupported. "+
			"Linux (Landlock) and macOS (Seatbelt) also keep secrets under HOME unreadable and block the network. "+
			"Windows (Low-integrity token) confines writes only: reads and the network stay open, so it runs only with AGENTGO_SANDBOX_OS_NET=1, "+
			"and the workspace folder is labelled permanently (undo: icacls <folder> /setintegritylevel M /T)")
	SandboxOSNet = newBool("AGENTGO_SANDBOX_OS_NET", false, GroupSandbox,
		"let OS-sandboxed commands use the network (off by default). Windows cannot block the network, so there this must be on for the sandbox to run at all")
	SandboxOSReadRoots = newStr("AGENTGO_SANDBOX_OS_READ", "", GroupSandbox,
		"extra read-only directories for OS-sandboxed commands (toolchains such as ~/go); separated like PATH; ignored on Windows, where reads are not confined")

	// Limits of one shell command (execute_bash and the other command runners).
	ShellTimeoutSec = newInt("AGENTGO_SHELL_TIMEOUT_SEC", 180, GroupSandbox,
		"default time limit of one execute_bash command, in seconds; the model may ask for up to 1800; 0 = no default limit")
	ShellMemoryMB = newInt("AGENTGO_SHELL_MEM_MB", -1, GroupSandbox,
		"memory cap shared by the processes of one command, in MB (Windows): -1 = 70% of physical memory, 0 = no cap")
	ShellMaxProcs = newInt("AGENTGO_SHELL_MAX_PROCS", 256, GroupSandbox,
		"processes one command may keep alive at once (Windows); 0 = no cap")

	WorkflowCheckpoint = newBool("AGENTGO_WORKFLOW_CHECKPOINT", true, GroupWorkflow, "checkpoint workflow runs (needed by ask_user nodes)")
	WorkflowGraphTool  = newBool("AGENTGO_WORKFLOW_GRAPH_TOOL", true, GroupWorkflow, "register workflows as Eino graph tools")
)

// --- debugging -------------------------------------------------------------

var (
	Dev             = newBool("AGENTGO_DEV", false, GroupDebug, "open the web inspector at startup (same as --dev)")
	DebugLLM        = newBool("AGENTGO_DEBUG_LLM", false, GroupDebug, "log the shape of every model request (roles, tool-call ids)")
	DebugLLMFile    = newStr("AGENTGO_DEBUG_LLM_FILE", "", GroupDebug, "append DEBUG_LLM lines to this file")
	DumpLLMDir      = newStr("AGENTGO_DUMP_LLM_DIR", "", GroupDebug, "write every model request body into this directory")
	CallTrace       = newBool("AGENTGO_CALL_TRACE", false, GroupDebug, "keep a per-call trace of each tool call (raw args, repaired args, policy, result, compaction, usage) for the debug view; also switchable at runtime")
	ArmorBreak      = newBool("AGENTGO_ARMOR_BREAK", false, GroupDebug, "enable local red-team harness preset (破甲); the reverse profile enables armor break by default without this flag")
	ArmorBreakExtra = newStr("AGENTGO_ARMOR_BREAK_EXTRA", "", GroupDebug, "optional extra system suffix appended only in 破甲 mode (local testing)")
	TestLLMBase     = newStr("AGENTGO_TEST_BASE", "", GroupDebug, "cmd/llmtest: API base to check instead of the saved configuration")
	TestLLMKey      = newStr("AGENTGO_TEST_KEY", "", GroupDebug, "cmd/llmtest: API key for AGENTGO_TEST_BASE")
	TestLLMModel    = newStr("AGENTGO_TEST_MODEL", "", GroupDebug, "cmd/llmtest: model name for AGENTGO_TEST_BASE")
)
