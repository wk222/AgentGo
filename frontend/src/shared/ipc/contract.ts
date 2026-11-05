// 前端 → Go AppService 的 IPC 契约：方法名 → [参数元组, 返回值]。
// 参数与 Go 方法签名一一对应（internal/bridge）；新增后端方法时先在这里登记，
// wailsCall 才会给出参数/返回类型检查。未登记的方法仍可用旧签名调用（返回 unknown）。
import type {
  APITestResult, AppInfo, ArtifactRecord, CallTraceEvent, BackgroundTask, CapabilityGrant, ChannelStatus, ConfigEntry, DiagnosticsExport, ExpertInfo, ExpertListReply, ForkSessionReply, GovernancePolicyReply, HooksStatus, IMLoginState, IMSaveRequest, IMStatus, MemoryContradiction, MemoryRecord, NewSessionWithExpertReply, SessionExpertReply,
  MemoryTreeNode, MemoryTreeOverview, MemoryGraphData, ApplyTruthReply, ContextReport, FeatureFlagsReply, FlowgramReply, LLMConfigReply, Reply, ResolveApplyTruthReply, SessionExport, SessionSummary, TerminalInfo,
  RunningWork, TruthResolution, UsageSummary, WorkflowDefinition, WorkspaceEntry, WorkspaceInfoReply,
} from './types'
import type { FileSearchResult, GitStatusItem, WorkspaceReviewChange } from '../../features/workspace/types'
import type { RollbackPreview, RollbackResult, TurnCheckpoint } from '../../stores/turnCheckpointModel'

type M<A extends unknown[], R> = { args: A; ret: R }

export interface IpcContract {
  SelectWorkspaceDirectory: M<[], {success: boolean; cancelled?: boolean; path?: string; error?: string}>
  GetCreationCenter: M<[], Record<string, any>>
  ExtensionVersions: M<[action: string, payload: string], Record<string, any>>
  // 会话 / 模式
  ListSessions: M<[], SessionSummary[]>
  NewSession: M<[title: string], Reply>
  DeleteSession: M<[sessionID: string], Reply>
  // 重命名：返回清理后（单行、≤80 字）实际保存的标题；导出：生成 Markdown，url 可下载，filename 是建议的保存名
  RenameSession: M<[sessionID: string, title: string], Reply<{ id?: string; title?: string }>>
  ExportSession: M<[sessionID: string], Reply<Partial<SessionExport>>>
  // 分叉：把整条对话（消息、模型上下文、模式、计划任务）复制成一条新对话，原对话不动；
  // 原对话正在运行或停在审批上时拒绝（success=false, busy=true）。title 为空则自动取「原标题（分叉）」
  ForkSession: M<[sessionID: string, title: string], Reply<ForkSessionReply>>
  // 专家：一份「这条对话以什么角色工作」的规则（官方内置 + 用户目录 + 工作区 .agentgo/experts/）。
  // 绑定只影响之后的消息，不改审批和沙箱；key 可以是 id 或名字，expert 为空 = 取消专家
  ListExperts: M<[], Reply<ExpertListReply>>
  GetExpert: M<[key: string], Reply<{ expert?: ExpertInfo }>>
  GetSessionExpert: M<[sessionID: string], Reply<SessionExpertReply>>
  SetSessionExpert: M<[sessionID: string, expert: string], Reply<SessionExpertReply>>
  NewSessionWithExpert: M<[title: string, expert: string], Reply<NewSessionWithExpertReply>>
  DeleteSessionsBatch: M<[sessionIDs: string[]], Reply<{ deleted?: number }>>
  DeleteOtherSessions: M<[keepSessionID: string], Reply<{ deleted?: number; keep_id?: string }>>
  DeleteSessionsByTitle: M<[title: string, keepSessionID: string], Reply<{ deleted?: number }>>
  GetSessionMessages: M<[sessionID: string], Reply>
  GetSessionModeForSession: M<[sessionID: string], Reply>
  SetSessionMode: M<[profile: string, canvas: string], Reply>
  SetSessionModeForSession: M<[sessionID: string, profile: string, canvas: string], Reply>
  StopSession: M<[sessionID: string], Reply>
  CancelEngineSession: M<[sessionID: string], Reply>
  CancelEngineRun: M<[runID: string], Reply>
  CancelAgentRun: M<[sessionID: string], Reply>
  SteerSession: M<[sessionID: string, instruction: string], Reply<{ steered?: boolean }>>
  UploadAttachment: M<[base64Data: string, mimeType: string], string>
  // 拖入聊天的文件：优先按「名称+大小+修改时间」找回原路径，找不到再复制进工作区 uploads/
  ResolveDroppedFile: M<[name: string, size: number, modifiedMs: number], {success: boolean; path?: string}>
  ImportChatFile: M<[name: string, base64Data: string], {success: boolean; path?: string; name?: string; bytes?: number; reused?: boolean; error?: string}>

  // 审批 / 交互
  ResolveApproval: M<[approvalID: string, approved: boolean, note: string, overrideArgs?: string], Reply>
  ApproveAndRemember: M<[approvalID: string, scope: 'call' | 'tool', note: string], Reply>
  AnswerQuestion: M<[interruptID: string, answerJSON: string], Reply>
  ResolveA2UIInteraction: M<[interactID: string, action: string, dataJSON: string], Reply>
  CancelA2UIInteraction: M<[interactID: string], Reply>

  // LLM / 设置
  GetContextReport: M<[sessionId: string], { success?: boolean; available?: boolean; session_id?: string; report?: ContextReport }>
  GetLLMConfig: M<[], LLMConfigReply>
  SetLLMConfig: M<[apiBase: string, apiKey: string, model: string, fallbackModel: string], Reply>
  TestLLM: M<[], APITestResult>
  LoadAzureLunaConfig: M<[], Reply>
  LoadDeepSeekConfig: M<[model: string], Reply>
  GetFeatureFlags: M<[], FeatureFlagsReply>
  // 操作确认方式 strict / balanced / open：改文件、执行命令前要不要逐次询问（设置后立即对新的工具调用生效）
  GetGovernancePolicy: M<[], GovernancePolicyReply>
  SetGovernanceControlMode: M<[mode: string], GovernancePolicyReply>
  GetMemorySettings: M<[], { total_tokens: number; automatic_tokens: number; hierarchy: boolean }>
  SetMemorySettings: M<[totalTokens: number, automaticTokens: number, hierarchy: boolean], Reply>
  GetConfigSnapshot: M<[], ConfigEntry[]>
  // 用量与诊断：近 N 天模型用量（按模型/按天）、当前版本、一键生成可发给维护人员的诊断包（不含对话/记忆内容与密钥）
  GetUsageSummary: M<[days: number], Reply<Partial<UsageSummary>>>
  GetAppInfo: M<[], AppInfo>
  ExportDiagnostics: M<[], Reply<Partial<DiagnosticsExport>>>
  // 用户钩子：<数据目录>/hooks.json 里列的脚本，在工具调用前后、收到消息时、一轮结束后运行。只读状态 + 立即重读
  GetHooksStatus: M<[], HooksStatus>
  ReloadHooks: M<[], HooksStatus>
  // 请求级调试串联：默认关闭；只在内存里留最近几个会话，密钥已隐藏
  GetCallTrace: M<[sessionID: string, runID: string, callID: string], Reply<{ enabled?: boolean; sessions?: string[]; events?: CallTraceEvent[] }>>
  SetCallTrace: M<[enabled: boolean], Reply<{ enabled?: boolean }>>
  ClearCallTrace: M<[sessionID: string], Reply>
  // 对话里交付的文件（deliver_file）：列出当前状态；移除后删除下载副本，内容不变的同一文件不会被再次交付
  ListSessionArtifacts: M<[sessionID: string], Reply<{ artifacts?: ArtifactRecord[] }>>
  RemoveArtifact: M<[sessionID: string, artifactID: string], Reply<{ artifact?: ArtifactRecord }>>
  // 聊天软件（Telegram/飞书/钉钉/企微/微信/Discord）：读写 <数据目录>/channels.yml 并立即重连；密钥只回「是否已填」。失败时 Promise 拒绝，消息是原因
  GetIMStatus: M<[], IMStatus>
  SaveIMChannel: M<[req: IMSaveRequest], IMStatus>
  RemoveIMChannel: M<[platform: string], IMStatus>
  ReloadIM: M<[], IMStatus>
  StartWeixinLogin: M<[], IMLoginState>
  GetWeixinLoginState: M<[], IMLoginState>
  CancelWeixinLogin: M<[], IMLoginState>
  ListRegisteredTools: M<[], string[]>
  ListSkills: M<[], Reply[]>
  GetSkillContext: M<[skillIDs: string[]], string>
  ListScheduledJobs: M<[], Reply[]>
  CreateScheduledJob: M<[title: string, spec: string, prompt: string, sessionID: string], Reply>
  ToggleScheduledJob: M<[id: string, enabled: boolean], Reply<{ enabled?: boolean }>>
  TriggerScheduledJobNow: M<[id: string], Reply>
  DeleteScheduledJob: M<[id: string], Reply>
  GetRuntimeThreadOverview: M<[], Reply>
  ClearFinishedTrackedRuns: M<[], Reply<{ cleared?: number }>>
  ListChannels: M<[], ChannelStatus[]>
  ListBackgroundTasks: M<[limit: number], BackgroundTask[]>
  StartBackgroundTask: M<[kind: string, sessionID: string, prompt: string], Reply>
  CancelBackgroundTask: M<[taskID: string], Reply>
  GetRunningWork: M<[], RunningWork>
  ListAdminTasks: M<[limit: number, offset: number], Reply[]>
  PauseAdminTask: M<[taskID: string], Reply>
  ResumeAdminTask: M<[taskID: string], Reply>
  CancelAdminTask: M<[taskID: string], Reply>
  RetryAdminTask: M<[taskID: string], Reply>
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
  // 按轮回退：每轮开始前后端按内容存一份工作区文件（只在内存里，重启即失效；超过 8 MB 的文件与
  // .git、node_modules 等被忽略的目录不在内）。List 只给当前工作区里还能回退的轮次；Preview 不改任何文件，
  // busy 非空表示现在有运行或待审批、不能回退；Rollback 回退前会自动存一份现状（undo_checkpoint_id 可用来撤销），
  // 个别文件写不回去时 success=false 但 result 仍在，要逐项告诉用户。
  ListTurnCheckpoints: M<[sessionID: string], Reply<{ enabled?: boolean; checkpoints?: TurnCheckpoint[] }>>
  PreviewTurnRollback: M<[sessionID: string, checkpointID: string], Reply<{ preview?: RollbackPreview; more?: number; busy?: string }>>
  RollbackToTurn: M<[sessionID: string, checkpointID: string], Reply<{ result?: RollbackResult; undo_checkpoint_id?: string; busy?: boolean }>>

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
  // 记忆图谱：mode=overview（关联最多的记忆）或 ego（center 的邻域，depth 1–3）；modalities 逗号分隔，空=全部；limit 1–300。只读。
  GetMemoryGraphView: M<[scope: string, mode: string, center: string, depth: number, modalities: string, limit: number], Reply<{ graph?: MemoryGraphData; not_found?: boolean }>>
  GetMemoryTree: M<[scope: string, nodeID: string], Reply<{ overview?: MemoryTreeOverview; node?: MemoryTreeNode; hierarchy?: boolean }>>
  LocateMemoryTreeNode: M<[scope: string, nodeID: string], Reply<{ chain?: MemoryTreeNode[] }>>
  RefreshMemoryTree: M<[scope: string], Reply<{ overview?: MemoryTreeOverview; hierarchy?: boolean }>>
  PreviewMemoryContext: M<
    [scope: string, query: string, tokens: number],
    Reply<{ prompt?: string; node_ids?: string[]; nodes?: MemoryTreeNode[]; budget?: number; tokens?: number; hierarchy?: boolean }>
  >
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
