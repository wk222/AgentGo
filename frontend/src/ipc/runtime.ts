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

export function loadRuntime(maxMs = 2500): Promise<WailsRuntime | null> {
  if (_rt) return Promise.resolve(_rt)
  if (_rtPromise) return _rtPromise

  _rtPromise = (async () => {
    const effectiveMaxMs = previewHost ? Math.min(maxMs, 350) : maxMs
    const deadline = Date.now() + effectiveMaxMs
    while (Date.now() < deadline) {
      try {
        const mod = await importRuntime(WAILS_RUNTIME_PATH)
        if (mod?.Call?.ByName) {
          _rt = mod as WailsRuntime
          return _rt
        }
      } catch {
        // Browser preview: /wails/runtime.js is not served by Vite.
      }
      await new Promise(r => setTimeout(r, 120))
    }
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
