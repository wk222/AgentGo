// 前端 → Go AppService 的 IPC 契约：方法名 → [参数元组, 返回值]。
// 参数与 Go 方法签名一一对应（internal/bridge）；新增后端方法时先在这里登记，
// wailsCall 才会给出参数/返回类型检查。未登记的方法仍可用旧签名调用（返回 unknown）。
import type {
  APITestResult, BackgroundTask, CapabilityGrant, ChannelStatus, MemoryContradiction, MemoryRecord,
  ApplyTruthReply, FeatureFlagsReply, FlowgramReply, LLMConfigReply, Reply, ResolveApplyTruthReply, SessionSummary, TerminalInfo,
  TruthResolution, WorkflowDefinition, WorkspaceEntry, WorkspaceInfoReply,
} from './types'
import type { FileSearchResult, GitStatusItem, WorkspaceReviewChange } from '../panels/workspace/types'

type M<A extends unknown[], R> = { args: A; ret: R }

export interface IpcContract {
  // 会话 / 模式
  ListSessions: M<[], SessionSummary[]>
  NewSession: M<[title: string], Reply>
  DeleteSession: M<[sessionID: string], Reply>
  GetSessionMessages: M<[sessionID: string], Reply>
  GetSessionModeForSession: M<[sessionID: string], Reply>
  SetSessionMode: M<[profile: string, canvas: string], Reply>
  SetSessionModeForSession: M<[sessionID: string, profile: string, canvas: string], Reply>
  StopSession: M<[sessionID: string], Reply>
  CancelEngineSession: M<[sessionID: string], Reply>
  SteerSession: M<[sessionID: string, instruction: string], Reply<{ steered?: boolean }>>
  UploadAttachment: M<[base64Data: string, mimeType: string], string>

  // 审批 / 交互
  ResolveApproval: M<[approvalID: string, approved: boolean, note: string, overrideArgs?: string], Reply>
  AnswerQuestion: M<[interruptID: string, answerJSON: string], Reply>
  ResolveA2UIInteraction: M<[interactID: string, action: string, dataJSON: string], Reply>
  CancelA2UIInteraction: M<[interactID: string], Reply>

  // LLM / 设置
  GetLLMConfig: M<[], LLMConfigReply>
  SetLLMConfig: M<[apiBase: string, apiKey: string, model: string, fallbackModel: string], Reply>
  TestLLM: M<[], APITestResult>
  LoadAzureLunaConfig: M<[], Reply>
  LoadDeepSeekConfig: M<[model: string], Reply>
  GetFeatureFlags: M<[], FeatureFlagsReply>
  ListRegisteredTools: M<[], string[]>
  ListSkills: M<[], Reply[]>
  GetSkillContext: M<[skillIDs: string[]], string>
  ListScheduledJobs: M<[], Reply[]>
  ListChannels: M<[], ChannelStatus[]>
  ListBackgroundTasks: M<[limit: number], BackgroundTask[]>
  ListCapabilityGrants: M<[kind: string], CapabilityGrant[]>
  TransitionCapabilityGrant: M<[id: string, status: string], void>
  VerifyCapabilityGrant: M<[id: string, result: string], void>

  // 工作区 / 文件 / Git / Review
  GetWorkspaceInfo: M<[], WorkspaceInfoReply>
  SetWorkspaceRoot: M<[path: string], WorkspaceInfoReply>
  ListWorkspace: M<[relPath: string], WorkspaceEntry[]>
  ListWorkspaceDir: M<[relPath: string], WorkspaceEntry[]>
  ReadWorkspaceFile: M<[relPath: string], Reply>
  WriteWorkspaceFile: M<[relPath: string, content: string], Reply>
  DeleteWorkspacePath: M<[relPath: string], Reply>
  RenameWorkspacePath: M<[oldPath: string, newPath: string], Reply>
  SearchFiles: M<[pattern: string, maxResults: number], string[]>
  WorkspaceDiagnostics: M<[], Reply<{ total?: number }>>
  WorkspaceFileDiff: M<[relPath: string], Reply<{ original_content?: string; modified_content?: string }>>
  WorkspaceGitStage: M<[relPath: string], Reply>
  WorkspaceGitUnstage: M<[relPath: string], Reply>
  BeginWorkspaceReview: M<[sessionID: string], Reply>
  AcceptAllWorkspaceReview: M<[sessionID: string], Reply>
  RejectAllWorkspaceReview: M<[sessionID: string], Reply>

  CreateWorkspaceDir: M<[relPath: string], Reply>
  CreateWorkspaceFile: M<[relPath: string], Reply>
  WorkspaceReadBase64: M<[relPath: string], Reply<{ data_url?: string; size?: number }>>
  WorkspaceSearch: M<
    [query: string, isRegex: boolean, matchCase: boolean, matchWholeWord: boolean, includePattern: string, excludePattern: string, maxResults: number],
    Reply<{ results?: FileSearchResult[]; totalMatches?: number; totalFiles?: number }>
  >
  GenerateInlineEdit: M<
    [filePath: string, language: string, selectedText: string, contextBefore: string, contextAfter: string, instruction: string],
    Reply<{ replacement?: string }>
  >
  WorkspaceGitStatus: M<[], Reply<{ branch?: string; staged?: GitStatusItem[]; unstaged?: GitStatusItem[]; untracked?: GitStatusItem[] }>>
  WorkspaceGitCommit: M<[message: string], Reply>
  WorkspaceGitBranches: M<[], Reply<{ branches?: string[]; current?: string }>>
  ListWorkspaceReview: M<[sessionID: string], Reply<{ active?: boolean; changes?: WorkspaceReviewChange[]; skipped_count?: number }>>
  WorkspaceReviewFile: M<[sessionID: string, relPath: string], Reply<{ original_content?: string; modified_content?: string }>>
  AcceptWorkspaceReviewFile: M<[sessionID: string, relPath: string], Reply>
  RejectWorkspaceReviewFile: M<[sessionID: string, relPath: string], Reply>

  // 终端
  TerminalCreate: M<[id: string, shell: string, cwd: string, cols: number, rows: number], TerminalInfo>
  ExecuteTerminalCommand: M<[command: string], Reply<{ stdout?: string }>>
  TerminalList: M<[], TerminalInfo[]>
  TerminalWrite: M<[id: string, data: string], void>
  TerminalResize: M<[id: string, cols: number, rows: number], void>
  TerminalClose: M<[id: string], void>

  // 记忆
  ListMemories: M<[limit: number], MemoryRecord[]>
  GetMemoryGraph: M<[centerID: string], Reply>
  MemoryContextPrompt: M<[scope: string], Reply>
  MemoryDistill: M<[scope: string], Reply>
  MemoryFeedback: M<[id: string, signal: string], Reply>
  MemoryGC: M<[], Reply>
  MemoryRecallQA: M<[query: string, scope: string, modality: string, limit: number], Reply>
  DetectMemoryContradictions: M<[newFact: string, scope: string], MemoryContradiction[]>
  ResolveMemoryTruth: M<[factAID: string, factBID: string], TruthResolution>
  ResolveAndApplyMemoryTruth: M<[factAID: string, factBID: string], ResolveApplyTruthReply>
  ApplyMemoryTruthResolution: M<[factAID: string, factBID: string, resolutionJSON: string], ApplyTruthReply>

  // Inner Apps
  ListInnerApps: M<[limit: number], Reply[]>
  InvokeInnerApp: M<[name: string, input: string, capability: string], Reply>
  OpenInnerAppSession: M<[name: string], Reply>
  OpenInnerAppWindow: M<[name: string], Reply>
  GetInnerAppPageHTML: M<[name: string], Reply>
  ReadInnerAppFile: M<[name: string, relPath: string], Reply>
  BuildInnerAppIteratively: M<[name: string, displayName: string, description: string, mode: string, workflowID: string, systemPrompt: string, maxIterations: number, overwrite: boolean], Reply>
  CallInnerAppSessionAPI: M<[name: string, sessionID: string, nonce: string, action: string, payloadJSON: string], Reply>

  // 工作流
  ListWorkflows: M<[], WorkflowDefinition[]>
  SaveWorkflow: M<[def: WorkflowDefinition], Reply>
  RunWorkflow: M<[workflowID: string, input: string], Reply>
  GetWorkflowFlowgram: M<[workflowID: string], FlowgramReply>
  SaveWorkflowFlowgram: M<[workflowID: string, flowgramJSON: string], Reply>
  EnsureWorkflowHappyPath: M<[], Reply>
  OpenWorkflowWindow: M<[workflowID: string], Reply>
}

export type IpcMethod = keyof IpcContract
export type IpcArgs<K extends IpcMethod> = IpcContract[K]['args']
export type IpcRet<K extends IpcMethod> = IpcContract[K]['ret']
