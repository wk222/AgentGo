// 由 wails.ts 机械拆分而来（见 docs/plan.md F6）。
import { previewHost } from './env'
import { mockEvents } from './mock/events'

export const SVC = 'agentgo/internal/bridge.AppService'
export const WAILS_RUNTIME_PATH = '/wails/runtime.js'
export const importRuntime = new Function('path', 'return import(path)') as (path: string) => Promise<any>

export type WailsRuntime = { Call: any; Events: any }

export let _rt: WailsRuntime | null = null
export let _rtPromise: Promise<WailsRuntime | null> | null = null
export let previewLogged = false

export function logPreviewOnce() {
  if (previewLogged) return
  previewLogged = true
  console.info('[AgentGo] 使用 localhost mock runtime；桌面版会自动切回 Wails v3 runtime。')
}

/** 绝对地址：避免 new Function 里的 import() 按创建它的脚本（而不是当前页面）去解析相对路径。 */
function runtimeURL(): string {
  try {
    return new URL(WAILS_RUNTIME_PATH, document.baseURI || window.location.href).href
  } catch {
    return WAILS_RUNTIME_PATH
  }
}

/**
 * localhost/127.0.0.1 既可能是 Vite 预览（没有 runtime，要立刻落到 mock），
 * 也可能是网页版（自带 runtime，只是首屏慢）。先问一次服务端，而不是靠 350ms 超时猜。
 */
async function runtimeServed(): Promise<boolean> {
  if (!previewHost) return true
  try {
    const r = await fetch(runtimeURL(), { cache: 'no-store' })
    return r.ok && /javascript/i.test(r.headers.get('content-type') || '')
  } catch {
    return false
  }
}

export function loadRuntime(maxMs = 2500): Promise<WailsRuntime | null> {
  if (_rt) return Promise.resolve(_rt)
  if (_rtPromise) return _rtPromise

  _rtPromise = (async () => {
    if (!(await runtimeServed())) return null
    const deadline = Date.now() + maxMs
    const url = runtimeURL()
    while (Date.now() < deadline) {
      try {
        const mod = await importRuntime(url)
        if (mod?.Call?.ByName) {
          _rt = mod as WailsRuntime
          return _rt
        }
      } catch {
        // Browser preview: /wails/runtime.js is not served by Vite.
      }
      await new Promise(r => setTimeout(r, 120))
    }
    // 服务端明明有 runtime 却没加载上：不要把失败缓存一辈子，下次调用再试。
    _rtPromise = null
    return null
  })()

  return _rtPromise
}

export function wailsEvents(): { On: (...a: any[]) => any } | null {
  if (_rt?.Events?.On) return _rt.Events
  if (previewHost) {
    logPreviewOnce()
    return mockEvents
  }
  return null
}

export async function waitForWailsEvents(maxMs = 3000) {
  const rt = await loadRuntime(maxMs)
  if (rt?.Events?.On) return rt.Events
  logPreviewOnce()
  return mockEvents
}
