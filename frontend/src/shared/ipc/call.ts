// Independent IPC operations do not share a global queue. Mutations to the same
// method remain ordered; reads and interactive controls start immediately.
import { SVC, loadRuntime } from './runtime'
import { mockCall } from './mock/call'
import type { IpcArgs, IpcMethod, IpcRet } from './contract'

const ipcQueues = new Map<string, Promise<unknown>>()
export function resetIpcQueue() { ipcQueues.clear() }

function normalize(method: string, params: unknown[]): unknown[] {
 const out = [...params]
 if (method === 'ResolveApproval' && out.length === 3) out.push('')
 return out
}

type CancelableCall = { cancel?: () => unknown }

async function invoke(method: string, params: unknown[], timeoutMs: number): Promise<unknown> {
 let pending: CancelableCall | undefined
 let expired = false
 let timer: ReturnType<typeof setTimeout> | undefined
 const operation = (async () => {
  const rt = await loadRuntime(Math.min(timeoutMs, 2500))
  if (expired) throw new Error(method + ' 等待运行时超时')
  if (rt?.Call?.ByName) {
   const ret = rt.Call.ByName(SVC + '.' + method, ...params)
   pending = ret
   return await ret
  }
  return mockCall(method, ...params)
 })()
 const timeout = new Promise<never>((_, reject) => {
  timer = setTimeout(() => {
   expired = true
   // Wails CancellablePromise forwards this request to the binding context.
   // A binding may already have committed side effects; never report rollback.
   reject(new Error(method + ' 超时 (' + timeoutMs + 'ms)，已请求取消；请确认执行状态后再重试'))
   try { Promise.resolve(pending?.cancel?.()).catch(() => {}) } catch { /* best effort */ }
  }, Math.max(1, timeoutMs))
 })
 try { return await Promise.race([operation, timeout]) }
 finally { if (timer !== undefined) clearTimeout(timer) }
}

const DIRECT_UI_METHODS = new Set<string>([
  'SelectWorkspaceDirectory',
  'TestLLM',
  'GetCreationCenter',
  'ExtensionVersions',
  'RunEngine',
  'SteerSession',
  'ResolveDroppedFile',
  'ImportChatFile',
  'PreviewMemoryContext',
  'BeginWorkspaceReview',
  'GetSessionMessages',
  'GetSessionModeForSession',
  'ListSessions',
  'NewSession',
  'DeleteSession',
  'RenameSession',
  'ForkSession',
  'DeleteSessionsBatch',
  'DeleteOtherSessions',
  'DeleteSessionsByTitle',
  'SetSessionMode',
  'SetSessionModeForSession',
  'ResolveApproval',
  'ApproveAndRemember',
  'AnswerQuestion',
  'ResolveA2UIInteraction',
  'CancelA2UIInteraction',
])


const extensionTimeouts = new Map<string, number>()

/**
 * Locally injected extensions (never part of the public contract) register their
 * own interactive methods: they start immediately and get their own timeout.
 */
export function registerDirectMethod(method: string, timeoutMs: number) {
 DIRECT_UI_METHODS.add(method)
 extensionTimeouts.set(method, timeoutMs)
}

/** Loosely typed call for methods that are not in the public contract. */
export function extensionCall<T = unknown>(method: string, timeoutMs: number, ...params: unknown[]): Promise<T> {
 return schedule(method, params, timeoutMs) as Promise<T>
}

// 批准审批 / 回答提问会在调用里把暂停的运行续跑到结束才返回（服务端上限 15 分钟，
// 进度和结果都走 engine:event）。用普通的 15 秒超时会把一次正常的续跑误报成失败，
// 还会取消这次调用，所以给它们比服务端上限更长的期限。
const RESUME_TIMEOUT_MS = 16 * 60 * 1000

function defaultTimeout(method: string): number {
 if (method === 'SelectWorkspaceDirectory') return 300000
 if (method === 'ResolveApproval' || method === 'ApproveAndRemember' || method === 'AnswerQuestion') return RESUME_TIMEOUT_MS
 const extra = extensionTimeouts.get(method)
 if (extra !== undefined) return extra
 if (method === 'TestLLM') return 65000
 if (method === 'ImportChatFile') return 60000
 // 回退要扫两遍工作区再写文件：15 秒的超时会把还在进行的回退误报成失败。
 if (method === 'PreviewTurnRollback' || method === 'RollbackToTurn') return 60000
 if (method === 'ExtensionVersions') return 120000
 return 15000
}

function schedule(method: string, params: unknown[], timeoutMs: number): Promise<unknown> {
 const args = normalize(method, params)
 if (DIRECT_UI_METHODS.has(method) || /^(Get|List|Read|Search|Stat|Inspect)/.test(method)) {
  return invoke(method, args, timeoutMs)
 }
 const previous = ipcQueues.get(method) ?? Promise.resolve()
 const task = () => invoke(method, args, timeoutMs)
 const next = previous.then(task, task)
 ipcQueues.set(method, next)
 const cleanup = () => { if (ipcQueues.get(method) === next) ipcQueues.delete(method) }
 next.then(cleanup, cleanup)
 return next
}

export function wailsCall<K extends IpcMethod>(method: K, ...params: IpcArgs<K>): Promise<IpcRet<K>>
export async function wailsCall(method: string, ...params: unknown[]): Promise<unknown> {
 return schedule(method, params, defaultTimeout(method))
}

export function wailsCallUrgent<K extends IpcMethod>(method: K, timeoutMs: number, ...params: IpcArgs<K>): Promise<IpcRet<K>>
export async function wailsCallUrgent(method: string, timeoutMs: number, ...params: unknown[]): Promise<unknown> {
 return invoke(method, normalize(method, params), timeoutMs)
}

export function wailsCallWithTimeout<K extends IpcMethod>(method: K, timeoutMs: number, ...params: IpcArgs<K>): Promise<IpcRet<K>>
export async function wailsCallWithTimeout(method: string, timeoutMs: number, ...params: unknown[]): Promise<unknown> {
 return schedule(method, params, timeoutMs)
}

/** Escape hatch for dynamic method names; callers narrow the result themselves. */
export function wailsCallRaw(method: string, ...params: unknown[]): Promise<unknown> {
 return schedule(method, params, defaultTimeout(method))
}
