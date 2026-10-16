// IPC 调用层：串行队列 + 紧急通道 + 超时。
// 方法必须先在 contract.ts 登记，才能通过 wailsCall 调用（参数/返回类型受检）；
// 动态方法名或临时调用请显式使用 wailsCallRaw。
import { SVC, loadRuntime } from './runtime'
import { mockCall } from './mock/call'
import type { IpcArgs, IpcMethod, IpcRet } from './contract'

let ipcQueue: Promise<unknown> = Promise.resolve()

export function resetIpcQueue() {
  ipcQueue = Promise.resolve()
}

function normalize(method: string, params: unknown[]): unknown[] {
  const out = [...params]
  // 后端 ResolveApproval 的可变参数 overrideArgs 必须存在；旧调用点只传 3 个。
  if (method === 'ResolveApproval' && out.length === 3) out.push('')
  return out
}

function isThenable(v: unknown): v is PromiseLike<unknown> {
  return !!v && typeof (v as { then?: unknown }).then === 'function'
}

async function invoke(method: string, params: unknown[], runtimeWaitMs: number): Promise<unknown> {
  const rt = await loadRuntime(runtimeWaitMs)
  if (rt?.Call?.ByName) {
    const full = `${SVC}.${method}`
    console.info('[AgentGo] IPC →', full)
    const ret = rt.Call.ByName(full, ...params)
    return isThenable(ret) ? await ret : ret
  }
  return mockCall(method, ...params)
}

function withTimeout<T>(p: Promise<T>, method: string, timeoutMs: number): Promise<T> {
  let timer: ReturnType<typeof setTimeout>
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error(`${method} 超时 (${timeoutMs}ms)`)), timeoutMs)
  })
  return Promise.race([p, timeout]).finally(() => clearTimeout(timer)) as Promise<T>
}

// ---- 普通调用：进入串行队列 ----
export function wailsCall<K extends IpcMethod>(method: K, ...params: IpcArgs<K>): Promise<IpcRet<K>>
export async function wailsCall(method: string, ...params: unknown[]): Promise<unknown> {
  const args = normalize(method, params)
  const task = () => invoke(method, args, 2500)
  const next = ipcQueue.then(task, task)
  ipcQueue = next.catch(() => {})
  return next
}

// ---- 紧急调用：绕过队列（停止/取消），带超时 ----
export function wailsCallUrgent<K extends IpcMethod>(method: K, timeoutMs: number, ...params: IpcArgs<K>): Promise<IpcRet<K>>
export async function wailsCallUrgent(method: string, timeoutMs: number, ...params: unknown[]): Promise<unknown> {
  return withTimeout(invoke(method, normalize(method, params), Math.min(timeoutMs, 2500)), method, timeoutMs)
}

// ---- 普通调用 + 超时（仍走串行队列）----
export function wailsCallWithTimeout<K extends IpcMethod>(method: K, timeoutMs: number, ...params: IpcArgs<K>): Promise<IpcRet<K>>
export async function wailsCallWithTimeout(method: string, timeoutMs: number, ...params: unknown[]): Promise<unknown> {
  return withTimeout(wailsCallRaw(method, ...params), method, timeoutMs)
}

/** 逃生口：方法名动态或尚未登记契约时使用，返回值需调用方自行收窄。 */
export function wailsCallRaw(method: string, ...params: unknown[]): Promise<unknown> {
  const args = normalize(method, params)
  const task = () => invoke(method, args, 2500)
  const next = ipcQueue.then(task, task)
  ipcQueue = next.catch(() => {})
  return next
}
