// 由 wails.ts 机械拆分而来（见 docs/plan.md F6）。
import { isBrowser } from '../env'

export type MockSession = {
  id: string
  title: string
  updated_at: string
  message_count: number
  messages: any[]
}


export const PREVIEW_SESSIONS_KEY = 'agentgo-preview-sessions-v2'
export const PREVIEW_MODE_KEY = 'agentgo-preview-mode'
export const PREVIEW_GLOBAL_MODE_KEY = 'agentgo-preview-global-mode'
export const PREVIEW_LLM_KEY = 'agentgo-preview-llm'
export const PREVIEW_WORKSPACE_ROOT_KEY = 'agentgo-preview-workspace-root'
export const MAX_PREVIEW_BYTES = 512 * 1024


export function nowISO() {
  return new Date().toISOString()
}

export function clone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v))
}

export function readJSON<T>(key: string, fallback: T): T {
  if (!isBrowser) return fallback
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return fallback
    return JSON.parse(raw)
  } catch {
    return fallback
  }
}

export function writeJSON(key: string, value: any) {
  if (!isBrowser) return
  try { localStorage.setItem(key, JSON.stringify(value)) } catch {}
}

export function seedSessions(): MockSession[] {
  const ts = nowISO()
  const messages = [
    {
      role: 'assistant',
      type: 'text',
      content: '这是 localhost 预览模式。没有 Wails 桌面容器时，也可以调试会话、模式选择、A2UI 卡片和右侧 Workspace 面板。',
    },
    {
      role: 'assistant',
      type: 'aui',
      component: 'metrics',
      content: 'AgentGo 预览指标',
      data: {
        metrics: [
          { label: '上下文文件', value: 3, change: 1 },
          { label: '可用 Skills', value: 5, change: 2 },
          { label: '内置应用', value: 4, change: 1 },
        ],
      },
    },
    {
      role: 'assistant',
      type: 'aui',
      component: 'list',
      content: '建议下一步',
      data: {
        items: [
          { title: '从右栏选择文件并加入上下文' },
          { title: '切换 Assistant / Admin 与 Focused / Deep 模式' },
          { title: '打开 Memory、Skills、Apps 面板执行维护动作' },
        ],
        actions: [{ label: '已了解', value: 'ok' }],
      },
      interact_id: 'preview-list-action',
    },
  ]
  return [{
    id: 'preview-session',
    title: 'localhost 预览会话',
    updated_at: ts,
    message_count: messages.length,
    messages,
  }]
}

export function getMockSessions(): MockSession[] {
  const sessions = readJSON<MockSession[]>(PREVIEW_SESSIONS_KEY, [])
  if (Array.isArray(sessions) && sessions.length) {
    return sessions.map(s => ({
      ...s,
      messages: Array.isArray(s.messages) ? s.messages : [],
      message_count: Array.isArray(s.messages) ? s.messages.length : Number(s.message_count || 0),
    }))
  }
  const seeded = seedSessions()
  writeJSON(PREVIEW_SESSIONS_KEY, seeded)
  return seeded
}

export function saveMockSessions(sessions: MockSession[]) {
  writeJSON(PREVIEW_SESSIONS_KEY, sessions.map(s => ({
    ...s,
    message_count: Array.isArray(s.messages) ? s.messages.length : Number(s.message_count || 0),
    updated_at: s.updated_at || nowISO(),
  })))
}

export function publicSessions() {
  return getMockSessions().map(({ messages, ...s }) => ({
    ...s,
    message_count: messages.length,
  }))
}

export function ensureSession(id?: string): MockSession {
  const sessions = getMockSessions()
  const sid = String(id || '').trim()
  let session = sessions.find(s => s.id === sid)
  if (!session) {
    session = {
      id: sid || `preview-${Date.now()}`,
      title: '新对话',
      updated_at: nowISO(),
      message_count: 0,
      messages: [],
    }
    sessions.unshift(session)
    saveMockSessions(sessions)
  }
  return session
}

export function appendMockMessage(sessionID: string, msg: any) {
  const sessions = getMockSessions()
  let session = sessions.find(s => s.id === sessionID)
  if (!session) {
    session = ensureSession(sessionID)
    sessions.unshift(session)
  }
  session.messages.push(msg)
  session.message_count = session.messages.length
  session.updated_at = nowISO()
  saveMockSessions(sessions)
}

export function modeKey(sessionID: string) {
  return `${PREVIEW_MODE_KEY}:${sessionID}`
}

export function defaultMode() {
  return readJSON(PREVIEW_GLOBAL_MODE_KEY, { profile: 'assistant', canvas: 'balanced' })
}

export function getSessionMode(sessionID: string) {
  return readJSON(modeKey(sessionID), defaultMode())
}
