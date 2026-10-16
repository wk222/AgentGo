// IPC 返回值的领域类型。字段与 Go 侧 json tag 对齐；
// Go 里返回 map[string]any 的方法统一用 Reply（success/error 必有语义，其余字段为 unknown，需要时再显式声明）。

import type { FlowgramDocument } from '../workflow/flowgramAdapter'

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

export interface TruthResolution {
  resolution: string
  explanation: string
  merged_content?: string
  applied_record_id?: string
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

export interface SessionSummary {
  id: string
  title?: string
  updated_at?: string
  message_count?: number
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

export interface BackgroundTask {
  id: string
  [k: string]: unknown
}
