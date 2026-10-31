// Transport-neutral runtime contract. UI frameworks supply IPC and event hooks.
export interface RuntimeEvent {
  run_id: string
  session_id: string
  seq: number
  type: string
  payload?: Record<string, any>
}

export interface RuntimeTransport {
  call(method: string, ...args: any[]): Promise<any>
  on(name: string, listener: (payload: any) => void): () => void
}

export class RuntimeClient {
  constructor(private transport: RuntimeTransport) {}

  async start(sessionID: string, input: string, images: string[] = [], engine = '') {
    if (!sessionID || !input.trim()) throw new Error('会话和消息不能为空')
    const result = await this.transport.call('RunEngine', engine, sessionID, input, images)
    if (!result?.success || !result?.run_id) throw new Error(result?.error || '运行未启动')
    return String(result.run_id)
  }

  cancel(sessionID: string) {
    return this.transport.call('CancelEngineSession', sessionID)
  }

  /**
   * 订阅引擎事件。currentSession 为 null 时接收所有会话的事件：
   * 后台会话的运行不会因为用户切到别的对话就收不到进度。
   */
  subscribe(currentSession: (() => string) | null, receive: (event: RuntimeEvent) => void) {
    const lastSeq = new Map<string, number>()
    return this.transport.on('engine:event', raw => {
      const event: RuntimeEvent = raw?.name === 'engine:event' ? raw.data : raw
      if (!event?.run_id) return
      if (currentSession && event.session_id !== currentSession()) return
      if (!Number.isSafeInteger(event.seq) || event.seq <= (lastSeq.get(event.run_id) || 0)) return
      lastSeq.set(event.run_id, event.seq)
      // Keep completion cursors for late duplicate delivery, with bounded memory.
      if (lastSeq.size > 256) lastSeq.delete(lastSeq.keys().next().value!)
      receive(event)
    })
  }
}
