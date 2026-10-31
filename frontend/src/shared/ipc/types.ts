// IPC 返回值的领域类型。字段与 Go 侧 json tag 对齐；
// Go 里返回 map[string]any 的方法统一用 Reply（success/error 必有语义，其余字段为 unknown，需要时再显式声明）。

import type { FlowgramDocument } from '../../features/workflow/model/flowgramAdapter'

/** 后端回复里约定俗成的字段（全部可选）。 */
export interface ReplyBase {
  success?: boolean
  error?: string
  message?: string
  content?: string
  html?: string
  output?: string
  summary?: string
  app_name?: string
}

/** Go 侧 map[string]any 的通用回复：已知字段有类型，其余字段是 unknown（禁止隐式 any）。 */
export type Reply<T extends object = {}> = ReplyBase & T & Record<string, unknown>

export interface WorkspaceInfo {
  success?: boolean
  root: string
  name: string
  git_root?: string
  branch?: string
  dirty_count?: number
  is_git?: boolean
  writable?: boolean
  warning?: string
  preview_limit_bytes?: number
  watch_mode?: string
}

export type WorkspaceInfoReply = WorkspaceInfo & { error?: string }
export type LLMConfigReply = Reply<{
  api_base?: string; model?: string; fallback_model?: string
  api_key_set?: boolean; api_key_hint?: string
  has_azure_luna?: boolean; has_deepseek?: boolean; azure_api_base?: string; azure_model?: string
}>
export type FeatureFlagsReply = Reply<{ mode_profile?: string; execution_canvas?: string }>
export type FlowgramReply = Reply<{ flowgram?: FlowgramDocument }>
export type ApplyTruthReply = Reply<{ record?: { id?: string }; resolution?: { applied_record_id?: string } }>
export type ResolveApplyTruthReply = Reply<{ record?: { id?: string }; resolution?: TruthResolution }>

export interface WorkspaceEntry {
  name: string
  path: string
  is_dir: boolean
  size: number
  mod_time: string
  git_status?: string
}

export interface MemoryRecord {
  id: string
  content: string
  scope: string
  modality: string
  metadata?: Record<string, unknown>
  status: string
  importance: number
  last_recall_at: number
  recall_count: number
  supersedes_id?: string
  is_canonical: boolean
  source_trust: number
  contradicted_by?: string
  created_at: number
  updated_at: number
}

export interface MemoryContradiction {
  existing_id: string
  explanation: string
}

/** One node of the summary tree the model navigates (Go: memory.TreeNodeView). */
export interface MemoryTreeNode {
  id: string
  label: string
  summary: string
  count: number
  level: number
  /** true = the abstract was written by the model; false = an excerpt of the children */
  complete: boolean
  record_id?: string
  child_count: number
  children?: MemoryTreeNode[]
  record?: {
    modality: string
    scope: string
    kind: string
    canonical: boolean
    importance: number
    created_at: number
    content: string
  }
}

/** Published tree of one memory scope (Go: memory.TreeOverview). */
export interface MemoryTreeOverview {
  scope: string
  ready: boolean
  stale: boolean
  reason?: 'empty' | 'not_built' | 'stale'
  refreshing: boolean
  records: number
  covered: number
  nodes: number
  branches: number
  summarized: number
  levels: number
  fanout: number
  node_tokens: number
  root?: MemoryTreeNode
}

export interface TruthResolution {
  resolution: string
  explanation: string
  merged_content?: string
  applied_record_id?: string
}

/** Token accounting of a set of model calls (Go: bridge.usageTotals). cache_hit_rate = cached / prompt, 0..1. */
export interface UsageTotals {
  calls: number
  prompt_tokens: number
  cached_tokens: number
  completion_tokens: number
  reasoning_tokens: number
  total_tokens: number
  cache_hit_rate: number
}
export interface UsageByModel extends UsageTotals { model: string }
export interface UsageByDay extends UsageTotals { date: string }
/** Model usage of the last N days, oldest day first (Go: bridge.GetUsageSummary). */
export interface UsageSummary {
  days: number
  from: string
  to: string
  totals: UsageTotals
  by_model: UsageByModel[]
  by_day: UsageByDay[]
}

/** Which build is running (Go: bridge.GetAppInfo). `host` is 'web' (browser build) or 'desktop'. */
export interface AppInfo {
  version: string
  go_version: string
  os: string
  os_version?: string
  arch: string
  tags?: string
  host: 'web' | 'desktop' | string
  data_dir?: string
  /** 上一次运行异常退出时才有（Go: applog.CrashInfo）：下次启动报告一次。 */
  previous_crash?: { file: string; time: string; size: number; headline: string }
}

/** A finished diagnostics bundle (Go: bridge.ExportDiagnostics). `url` downloads it, `path` is where it lies. */
export interface DiagnosticsExport {
  name: string
  path: string
  size: number
  url: string
  files: string[]
}

/** 用户钩子的事件（Go: userhooks.Events）：工具调用前后、收到消息时、一轮结束后。 */
export type HookEvent = 'PreToolUse' | 'PostToolUse' | 'UserPromptSubmit' | 'Stop'

/** hooks.json 里配置的一个钩子（Go: userhooks.HookInfo）。 */
export interface HookInfo {
  event: HookEvent | string
  matcher?: string
  command: string
  timeout_s: number
  /** 脚本自己出错或超时时：continue 放行并警告，block 拦截。 */
  on_failure: 'continue' | 'block' | string
}

/** 一次钩子运行的记录（Go: userhooks.Run），内存里最近 200 条，新的在前。 */
export interface HookRun {
  seq: number
  at: string
  event: HookEvent | string
  tool?: string
  hook: string
  duration_ms: number
  exit_code: number
  result: 'ok' | 'blocked' | 'failed' | 'timeout' | 'canceled' | string
  reason?: string
}

/** hooks.json 里不能用的一项和原因（Go: userhooks.Problem）；其余的钩子照常生效。 */
export interface HookProblem {
  where: string
  message: string
}

/** 设置页「钩子」卡片的数据（Go: bridge.GetHooksStatus / ReloadHooks，即 userhooks.Status）。 */
export interface HooksStatus {
  enabled: boolean
  disabled_reason?: string
  path: string
  exists: boolean
  loaded_at?: string
  sha256?: string
  counts: Record<string, number>
  total: number
  hooks: HookInfo[]
  problems: HookProblem[]
  /** 文件读不了或不是合法 JSON；上一份有效配置继续生效。 */
  error?: string
  recent: HookRun[]
}

/** 调用追踪的一步（Go: agent.CallTraceEvent）。call_id 为空的是整次运行级的步骤（压缩、用量）。 */
export interface CallTraceEvent {
  seq: number
  session_id: string
  run_id: string
  call_id?: string
  tool?: string
  stage: 'model_args' | 'args_repaired' | 'policy' | 'result' | 'compaction' | 'usage'
  at: number
  duration_ms?: number
  data?: Record<string, unknown>
}

/** 对话里交付的一个文件（Go: artifact.Record）。status=removed 表示用户已把它从交付物里移除。 */
export interface ArtifactRecord {
  id: string
  path: string
  name: string
  title?: string
  size: number
  sha256: string
  version: number
  status: 'new' | 'new_version' | 'unchanged' | 'removed'
  snapshot: string
  url: string
  at: string
  attached: boolean
  note?: string
}

/** 聊天软件渠道的一项配置字段（Go: im.FieldInfo）。 */
export interface IMField { key: string; label: string; secret: boolean; hint?: string }

/** 界面可配置的聊天平台（Go: im.PlatformInfo）。qr_login 表示要扫码登录（微信）。 */
export interface IMPlatform { id: string; label: string; fields: IMField[] | null; qr_login: boolean; help?: string }

/** channels.yml 里的一个渠道（Go: im.ChannelView）。密钥只有「是否已填」，没有值。 */
export interface IMChannel {
  platform: string
  enabled: boolean
  running: boolean
  allow_all: boolean
  allow_from: string[]
  fields: Record<string, string>
  secrets_set: Record<string, boolean>
  logged_in: boolean
  problems?: string[]
}

/** 设置页「聊天软件」卡片的数据（Go: bridge.GetIMStatus，即 im.AdminStatus）。 */
export interface IMStatus {
  config_path: string
  disabled: boolean
  disabled_reason?: string
  platforms: IMPlatform[]
  channels: IMChannel[]
  error?: string
}

/** 保存一个渠道（Go: im.SaveRequest）。密钥留空 = 保持原值。 */
export interface IMSaveRequest {
  platform: string
  enabled: boolean
  allow_all: boolean
  allow_from: string[]
  fields: Record<string, string>
}

/** 微信扫码登录的进度（Go: im.LoginState）。 */
export interface IMLoginState {
  status: 'idle' | 'waiting' | 'scanned' | 'expired' | 'done' | 'error'
  qr_data_url?: string
  message?: string
  user_id?: string
}

/** A conversation written as a Markdown file (Go: bridge.ExportSession). `filename` is what the saved file should be called. */
export interface SessionExport {
  name: string
  filename: string
  path: string
  size: number
  url: string
  messages: number
}

/** 操作确认方式（Go: governance.ControlMode）：strict 最谨慎，open 改文件不再逐次询问。 */
export type ControlMode = 'strict' | 'balanced' | 'open'

/** Go: bridge.GetGovernancePolicy / SetGovernanceControlMode 的共同回答。 */
export interface GovernancePolicyReply {
  success?: boolean
  error?: string
  control_mode?: ControlMode | string
  modes?: string[]
  policy?: Record<string, unknown>
}

export interface ConfigEntry {
  name: string
  kind: string
  default: string
  group: string
  doc: string
  secret?: boolean
  value: string
  set: boolean
}

export interface APITestResult {
  ok: boolean
  message: string
  models?: string[]
  http_code?: number
}

/** Mirrors agent.ContextBudgetReport (internal/agent/context_inspector*.go). */
export interface ContextReport {
  total_tokens: number
  budget_limit: number
  model_context_window?: number
  reserved_output_tokens?: number
  window_percent?: number
  segments: { source: string; token_count: number; byte_count?: number; percentage: number }[]
  top_items?: { source: string; label: string; token_count: number }[]
  turns?: { index: number; title: string; messages: number; token_count: number }[]
  advice?: string[]
}

export interface SessionSummary {
  id: string
  title?: string
  updated_at?: string
  message_count?: number
  /** 只有分叉出来的对话才有：来自哪条对话；原对话被删后 parent_gone 为 true，parent_title 是分叉时记下的标题。 */
  parent_id?: string
  parent_title?: string
  parent_gone?: boolean
}

/** 分叉的回答（Go: bridge.ForkSession）。busy = 原对话正在运行或停在审批上：不是故障，请用户先处理。 */
export interface ForkSessionReply {
  id?: string
  session?: SessionSummary
  busy?: boolean
  parent_id?: string
  parent_title?: string
  messages?: number
  events?: number
  tasks?: number
  warnings?: string[]
}

/** 一行「运行中」：会话、没有会话的引擎运行、后台任务，或停着等用户的东西（Go: bridge.RunningWorkItem）。 */
export interface RunningWorkItem {
  kind: 'session' | 'run' | 'task' | 'waiting'
  /** 列表内唯一。 */
  id: string
  /** 对应的对话（任务在哪个对话里干活也算）。 */
  session_id?: string
  /** 这个对话还在列表里，能打开；内部会话、已删除的是 false。 */
  known: boolean
  /** 会话：它的标题；任务：它被要求做什么（一行）。 */
  title?: string
  /** 任务：它所在对话的标题。 */
  session_title?: string
  state: 'running' | 'queued' | 'awaiting_approval' | 'awaiting_answer'
  /** 开始/开始等待的时间（unix 秒），未知为空。 */
  since?: number
  /** 等待中：它想执行的工具。 */
  tool?: string
  /** 等待中：这个对话里停着几项。 */
  count?: number
  /** 没有对话的等待：workflow | matrix。 */
  source?: string
  task_id?: string
  task_kind?: string
  run_id?: string
  engine?: string
  stoppable: boolean
}

/** GetRunningWork 的返回（Go: bridge.RunningWork）。 */
export interface RunningWork {
  items: RunningWorkItem[]
  /** 在跑的（含排队）。 */
  running: number
  /** 停着等用户的。 */
  waiting: number
  /** 服务端时间（unix 秒）；耗时用它算，不受浏览器时钟影响。 */
  at: number
}

export interface CapabilityGrant {
  id: string
  status?: string
  kind?: string
  [k: string]: unknown
}

export interface WorkflowDefinition {
  id: string
  name: string
  description?: string
  nodes?: unknown[]
  edges?: unknown[]
  [k: string]: unknown
}

export interface ChannelStatus {
  id: string
  channel?: string
  kind?: string
  state?: string
  [k: string]: unknown
}

export interface TerminalInfo {
  id: string
  shell?: string
  title?: string
  [k: string]: unknown
}

/** Go: taskhub.Task。status：queued | running | completed | failed | cancelled。 */
export interface BackgroundTask {
  id: string
  kind?: string
  session_id?: string
  input?: string
  status?: string
  result?: string
  error?: string
  created_at?: number
  updated_at?: number
  [k: string]: unknown
}

/** One memory in the link graph (Go: memory.GraphNode). degree counts all its links, shown or not. */
export interface MemoryGraphNode {
  id: string
  title: string
  modality: string
  scope: string
  importance: number
  canonical?: boolean
  degree: number
}

export interface MemoryGraphEdge {
  source: string
  target: string
  relation?: string
  weight: number
}

/** How the reply relates to the whole graph (Go: memory.GraphMeta). */
export interface MemoryGraphMeta {
  mode: 'overview' | 'ego'
  scope: string
  /** linked memories that match, before the limit (for ego: reached before the limit stopped the walk) */
  total: number
  returned: number
  truncated: boolean
  /** matching memories with no link at all; never drawn */
  isolated: number
  center?: string
  depth?: number
  groups: Record<string, number>
}

export interface MemoryGraphData {
  nodes: MemoryGraphNode[]
  edges: MemoryGraphEdge[]
  meta: MemoryGraphMeta
}
