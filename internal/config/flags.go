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
		"startup session profile (assistant|app_matrix|admin)")
	ExecutionCanvas = newStr("AGENTGO_EXECUTION_CANVAS", "", GroupAgent,
		"startup execution canvas (focused|balanced|deep)")
	WorkspaceRoot = newStr("AGENTGO_WORKSPACE_ROOT", "", GroupAgent,
		"workspace directory the agent works in (overrides the remembered one)")
	Workspace = newStr("AGENTGO_WORKSPACE", "", GroupAgent, "workspace used by memory journaling hooks")

	DisableAgentsMD = newBool("AGENTGO_DISABLE_AGENTSMD", false, GroupAgent, "do not inject AGENTS.md files")
	EinoSkillMW     = newBool("AGENTGO_EINO_SKILL_MW", false, GroupAgent, "mount the Eino skill middleware")
	EinoPlanTaskMW  = newBool("AGENTGO_EINO_PLANTASK", false, GroupAgent, "legacy alias gate for the plan-task middleware")
	EinoBGTask      = newBool("AGENTGO_EINO_BG_TASK", true, GroupAgent, "background task control middleware")
	EinoPlanTask    = newBool("AGENTGO_EINO_PLAN_TASK", true, GroupAgent, "plan task middleware (TaskCreate/Update/...)")
	EinoSpill       = newBool("AGENTGO_EINO_SPILL", true, GroupAgent, "offload large tool outputs to the spill store")
	TurnLoop        = newBool("AGENTGO_TURN_LOOP", false, GroupAgent, "run chats through the ADK TurnLoop")
	ToolHeal        = newBool("AGENTGO_TOOL_HEAL", true, GroupAgent, "repair malformed tool-call JSON and retry")

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

var (
	AutoReflect  = newBool("AGENTGO_AUTO_REFLECT", false, GroupMemory, "reflect on each turn after it finishes")
	AutoGC       = newBool("AGENTGO_AUTO_GC", false, GroupMemory, "garbage-collect memory after turns")
	MemoryGarden = newBool("AGENTGO_MEMORY_GARDEN", false, GroupMemory, "run the memory garden after turns")
	AutoJournal  = newBool("AGENTGO_AUTO_JOURNAL", false, GroupMemory, "write a diary entry after turns")

	EmbeddingModel = newStr("AGENTGO_EMBEDDING_MODEL", "text-embedding-3-small", GroupMemory, "embedding model name")
	EmbeddingDim   = newInt("AGENTGO_EMBEDDING_DIM", 1536, GroupMemory, "embedding vector dimension")
	MilvusAddr     = newStr("AGENTGO_MILVUS_ADDR", "", GroupMemory, "Milvus address; empty = vector store disabled")
	MilvusColl     = newStr("AGENTGO_MILVUS_COLLECTION", "agentgo_memory", GroupMemory, "Milvus collection name")

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

	CrushprotoAddr   = newStr("AGENTGO_CRUSHPROTO_ADDR", "", GroupGateway, "serve the Crush protocol on this address; empty = off")
	CrushprotoEngine = newStr("AGENTGO_CRUSHPROTO_ENGINE", "", GroupGateway, "engine id used by the Crush protocol")
	CrushprotoYolo   = newBool("AGENTGO_CRUSHPROTO_YOLO", false, GroupGateway, "auto-grant permission prompts from the Crush TUI")

	Hostlink    = newBool("AGENTGO_HOSTLINK", true, GroupGateway, "loopback host endpoint other surfaces attach to")
	HostlinkLog = newBool("AGENTGO_HOSTLINK_LOG", false, GroupGateway, "log every host protocol request")
)

// --- plugins / background services ----------------------------------------

var (
	PluginsDisable  = newStr("AGENTGO_PLUGINS_DISABLE", "", GroupPlugins, "comma-separated plugin names to switch off")
	CodeTools       = newBool("AGENTGO_CODETOOLS", true, GroupPlugins, "code-aware tools plugin")
	CodeToolsAppr   = newBool("AGENTGO_CODETOOLS_APPROVE", true, GroupPlugins, "0 denies every mutating code tool (read-only)")
	CodeToolsNative = newBool("AGENTGO_CODETOOLS_NATIVE", true, GroupPlugins, "0 falls back to the Crush-backed view/write/edit/multiedit")
	OfficeDoc       = newBool("AGENTGO_OFFICEDOC", true, GroupPlugins, "Word/Excel generation plugin")
	LegacyDynExec   = newBool("AGENTGO_LEGACY_DYNAMIC_EXEC", false, GroupPlugins, "use the legacy dynamic tool executor")

	DisableScheduler = newBool("AGENTGO_DISABLE_SCHEDULER", false, GroupPlugins, "do not start the cron scheduler (headless hosts)")
	DisableTriggers  = newBool("AGENTGO_DISABLE_TRIGGERS", false, GroupPlugins, "do not start trigger watchers")

	MCPCommand = newStr("AGENTGO_MCP_COMMAND", "", GroupPlugins, "start one MCP server with this command at boot")
	MCPArgs    = newStr("AGENTGO_MCP_ARGS", "", GroupPlugins, "space-separated arguments for MCP_COMMAND")
)

// --- sandbox / workflow ----------------------------------------------------

var (
	SandboxMode        = newStr("AGENTGO_SANDBOX_MODE", "", GroupSandbox, "execution seam: local (default) or docker")
	SandboxDocker      = newBool("AGENTGO_SANDBOX_DOCKER", false, GroupSandbox, "run bash/python tools inside Docker")
	SandboxImageBash   = newStr("AGENTGO_SANDBOX_IMAGE_BASH", "node:20-alpine", GroupSandbox, "Docker image for bash tools")
	SandboxImagePython = newStr("AGENTGO_SANDBOX_IMAGE_PYTHON", "", GroupSandbox, "Docker image for python tools")

	WorkflowCheckpoint = newBool("AGENTGO_WORKFLOW_CHECKPOINT", true, GroupWorkflow, "checkpoint workflow runs (needed by ask_user nodes)")
	WorkflowGraphTool  = newBool("AGENTGO_WORKFLOW_GRAPH_TOOL", true, GroupWorkflow, "register workflows as Eino graph tools")
)

// --- debugging -------------------------------------------------------------

var (
	Dev          = newBool("AGENTGO_DEV", false, GroupDebug, "open the web inspector at startup (same as --dev)")
	DebugLLM     = newBool("AGENTGO_DEBUG_LLM", false, GroupDebug, "log the shape of every model request (roles, tool-call ids)")
	DebugLLMFile = newStr("AGENTGO_DEBUG_LLM_FILE", "", GroupDebug, "append DEBUG_LLM lines to this file")
	DumpLLMDir   = newStr("AGENTGO_DUMP_LLM_DIR", "", GroupDebug, "write every model request body into this directory")
)
