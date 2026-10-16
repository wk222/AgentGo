// 由 wails.ts 机械拆分而来（见 docs/plan.md F6）。

export type EventHandler = (payload: any) => void


export const mockHandlers = new Map<string, Set<EventHandler>>()

export const mockEvents = {
  On(event: string, cb: EventHandler) {
    const set = mockHandlers.get(event) || new Set<EventHandler>()
    set.add(cb)
    mockHandlers.set(event, set)
    return () => set.delete(cb)
  },
}

export function emitMock(event: string, payload: any = {}) {
  const set = mockHandlers.get(event)
  if (!set?.size) return
  for (const cb of Array.from(set)) {
    try { cb(payload) } catch (e) { console.error('[AgentGo] mock event handler:', e) }
  }
}
