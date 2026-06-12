import { ref, nextTick } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import {
  wailsCall,
  wailsCallUrgent,
  wailsCallWithTimeout,
  wailsEvents,
  waitForWailsEvents,
  resetIpcQueue,
} from '../wails'

marked.use({ breaks: true, gfm: true })

export interface Session {
  id?: string
  session_id?: string
  title?: string
  name?: string
  model?: string
  message_count?: number
  messageCount?: number
  updated?: string
  updated_at?: string
}

export interface Message {
  role: 'user' | 'assistant' | 'system'
  type: 'text' | 'aui' | 'approval' | 'tool_call' | 'question'
  content?: string
  html?: string
  streaming?: boolean
  component?: string
  surface?: string
  data?: any
  interactId?: string
  resolved?: boolean
  approval_id?: string
  toolName?: string
  arguments?: string
  status?: string
  meta?: any
  _key?: string
}

export function sessionIdOf(s: Session | null | undefined): string {
  return String(s?.id || s?.session_id || '').trim()
}

export function sessionTitle(s: Session | null | undefined): string {
  const t = String(s?.title || s?.name || '').trim()
  return t || '新对话'
}

function renderMarkdown(text: string): string {
  try {
    const html = marked(text || '') as string
    return DOMPurify.sanitize(html, { USE_PROFILES: { html: true } })
  } catch {
    return DOMPurify.sanitize(text || '')
  }
}

function stripToolLeak(text: string): string {
  return text.replace(/<tool_call>[\s\S]*?<\/tool_call>/g, '').trim()
}

function isPlaceholderMessage(m: Message): boolean {
  const c = String(m?.content || '')
  return m?.role === 'assistant' && (c.includes('就绪') || c.includes('加载会话'))
}

function messagesHaveRealContent(msgs: Message[]): boolean {
  return (msgs || []).some(m => {
    if (isPlaceholderMessage(m)) return false
    if (m.type === 'aui') return true
    if (m.role === 'user' && String(m.content || '').trim()) return true
    if (m.role === 'assistant' && (m.streaming || String(m.content || '').trim())) return true
    return false
  })
}

function makeAUIMessage(payload: any): Message {
  let data = payload?.data || {}
  if (typeof data === 'string') { try { data = JSON.parse(data) } catch { data = {} } }
  if (typeof payload?.data_json === 'string') {
    try { data = { ...JSON.parse(payload.data_json), ...data } } catch {}
  }
  const surface = String(payload?.surface || data?.surface || 'chat').toLowerCase()
  return {
    role: 'assistant',
    type: 'aui',
    component: String(payload?.component || payload?.type || 'card').toLowerCase(),
    surface,
    data: surface && surface !== 'chat' ? { ...data, __surface: surface } : data,
    interactId: payload?.interact_id || payload?.interactId || '',
    content: payload?.title || payload?.heading || payload?.name || '',
    _key: '',
  }
}

export function useChat() {
  const messages = ref<Message[]>([])
  const sessions = ref<Session[]>([])
  const sessionId = ref('')
  const sessionLoading = ref(false)
  const sending = ref(false)
  const awaitingInteract = ref(false)
  const runStatusLine = ref('')
  const chatPaneKey = ref(0)
  const streamMsgIndex = ref(-1)
  const formInputs = ref<Record<string, Record<string, any>>>({})
  const activeInteractId = ref('')

  const messageCache = new Map<string, Message[]>()
  let sessionLoadSeq = 0
  let activeStreamSession = ''
  let unsubs: Array<() => void> = []
  let cleanupFn: (() => void) | null = null

  const bumpPane = () => { chatPaneKey.value++ }

  const sessionMsgCount = (id: string) => {
    const s = sessions.value.find(s => sessionIdOf(s) === id)
    return Number(s?.message_count || s?.messageCount || 0)
  }

  const welcomeMsg = (): Message[] => [{
    role: 'assistant',
    type: 'text',
    content: '新会话已就绪，输入消息开始对话。',
    _key: `welcome_${Date.now()}`,
  }]

  const deepClone = (msgs: Message[]) => (msgs || []).map(m => ({ ...m }))

  const stampKeys = (msgs: Message[], sid: string) =>
    (msgs || []).map((m, i) => ({
      ...m,
      _key: m._key || `${sid}_${chatPaneKey.value}_${i}_${m.role}`,
    }))

  const putCache = (id: string, msgs: Message[]) => messageCache.set(id, deepClone(msgs))
  const getCache = (id: string) => messageCache.get(id) || null

  const applyMessages = (id: string, msgs: Message[]) => {
    if (sessionId.value !== id) return
    messages.value = stampKeys(deepClone(msgs), id)
    putCache(id, msgs)
  }

  const endStreamingUI = (msg?: string) => {
    sending.value = false
    awaitingInteract.value = false
    streamMsgIndex.value = -1
    runStatusLine.value = msg || ''
    for (const m of messages.value) {
      if (m.streaming) {
        m.streaming = false
        if ((m.content || '').trim()) m.html = renderMarkdown(m.content!)
      }
    }
  }

  const mapRow = (m: any): Message => {
    const t = String(m.type || m.msg_type || '').toLowerCase()
    if (t === 'aui' || m.component || m.data_json || m.dataJson) {
      return makeAUIMessage({ ...m, type: 'aui' })
    }
    const content = stripToolLeak(m.content || '')
    return {
      role: m.role,
      type: m.type || 'text',
      content,
      html: (m.type || 'text') === 'text' ? renderMarkdown(content) : '',
      approval_id: m.approval_id,
      toolName: m.tool_name,
      arguments: m.arguments,
      status: m.status,
      meta: m.images ? { images: m.images } : undefined,
    }
  }

  const rowsToMessages = (rows: any[], sid: string): Message[] => {
    const mapped = (rows || [])
      .filter(r => {
        const rs = String(r.session_id || r.sessionId || '').trim()
        return !sid || !rs || rs === sid
      })
      .map(mapRow)
      .filter(m => m.type !== 'text' || !!(m.content || '').trim())
    return mapped.length ? mapped : welcomeMsg()
  }

  const appendServerMessages = (rows: any[], sid: string) => {
    const mapped = rowsToMessages(rows || [], sid)
      .filter(m => !isPlaceholderMessage(m))
      .filter(m => m.type !== 'text' || !!(m.content || '').trim())
    if (!mapped.length || sessionId.value !== sid) return
    const stamped = stampKeys(mapped, sid)
    messages.value = [...messages.value, ...stamped]
    putCache(sid, messages.value)
  }

  const parseResp = (resp: any, sid: string): Message[] => {
    if (!resp) return welcomeMsg()
    let rows: any[] = []
    if (Array.isArray(resp)) rows = resp
    else if (Array.isArray(resp?.messages)) rows = resp.messages
    else if (Array.isArray(resp?.rows)) rows = resp.rows
    else if (Array.isArray(resp?.data)) rows = resp.data
    return rowsToMessages(rows, sid)
  }

  const loadSessions = async () => {
    try {
      const list = await wailsCall('ListSessions') || []
      sessions.value = list
    } catch { sessions.value = [] }
  }

  const newSession = async () => {
    if (sending.value || awaitingInteract.value) await stopGeneration()
    endStreamingUI()
    sessionLoadSeq++
    bumpPane()
    activeStreamSession = ''
    streamMsgIndex.value = -1
    try {
      const r = await wailsCall('NewSession', '新对话')
      if (r?.success && (r.session || r.id)) {
        const sid = sessionIdOf(r.session) || String(r.id || '').trim()
        if (!sid) throw new Error('NewSession 未返回 id')
        sessionId.value = sid
        messages.value = welcomeMsg()
        sessionLoading.value = false
        localStorage.setItem('agentgo-last-session', sid)
        await loadSessions()
      }
    } catch (e: any) { console.error('[AgentGo] newSession:', e?.message) }
  }

  const selectSession = async (id: string) => {
    id = String(id || '').trim()
    if (!id) return
    if (sessionId.value === id && messagesHaveRealContent(messages.value)) return
    if (sending.value || awaitingInteract.value) await stopGeneration()

    if (sessionId.value && messages.value.length) putCache(sessionId.value, messages.value)

    sessionLoadSeq++
    bumpPane()
    activeStreamSession = ''
    streamMsgIndex.value = -1
    sessionId.value = id
    localStorage.setItem('agentgo-last-session', id)

    const hasHistory = sessionMsgCount(id) > 0
    const mySeq = sessionLoadSeq
    const cached = getCache(id)
    sessionLoading.value = !!hasHistory

    if (cached?.length && messagesHaveRealContent(cached)) {
      applyMessages(id, cached)
    } else {
      messages.value = []
    }

    try {
      await nextTick()
      const resp = await wailsCallWithTimeout('GetSessionMessages', 8000, id)
      if (mySeq !== sessionLoadSeq || sessionId.value !== id) return
      const next = parseResp(resp, id)
      if (next.length) applyMessages(id, next)
      else if (!messagesHaveRealContent(messages.value)) applyMessages(id, welcomeMsg())
    } catch (e: any) {
      if (mySeq !== sessionLoadSeq || sessionId.value !== id) return
      applyMessages(id, [{ role: 'assistant', type: 'text', content: '加载失败: ' + e.message }])
    } finally {
      if (mySeq === sessionLoadSeq && sessionId.value === id) sessionLoading.value = false
    }
  }

  const stopGeneration = async () => {
    const sid = sessionId.value || activeStreamSession
    endStreamingUI()
    resetIpcQueue()
    const interactID = activeInteractId.value
    activeInteractId.value = ''
    if (interactID) { try { await wailsCallUrgent('CancelA2UIInteraction', 1500, interactID) } catch {} }
    if (sid) { try { await wailsCallUrgent('StopSession', 3000, sid) } catch {} }
  }

  const sendMessage = async (text: string, images?: string[]) => {
    text = String(text || '').trim()
    if (!text || sending.value) return

    if (!sessionId.value) {
      try {
        const r = await wailsCall('NewSession', '新对话')
        const sid = sessionIdOf(r?.session) || String(r?.id || '').trim()
        if (!r?.success || !sid) return
        sessionId.value = sid
        await loadSessions()
      } catch { return }
    }

    const sid = sessionId.value
    activeStreamSession = sid

    messages.value.push({
      role: 'user', type: 'text', content: text, html: '',
      meta: images && images.length > 0 ? { images } : undefined,
      _key: `${sid}_${chatPaneKey.value}_user_${Date.now()}`,
    })

    const streamMsg: Message = {
      role: 'assistant', type: 'text', content: '', html: '', streaming: true,
      _key: `${sid}_${chatPaneKey.value}_stream_${Date.now()}`,
    }
    messages.value.push(streamMsg)
    streamMsgIndex.value = messages.value.length - 1

    sending.value = true
    runStatusLine.value = '思考中…'
    resetIpcQueue()

    try {
      await wailsCall('SendMessageStream', sid, text, images || [])
    } catch (e: any) {
      endStreamingUI('发送失败: ' + e.message)
      const idx = streamMsgIndex.value
      if (idx >= 0 && messages.value[idx]?.streaming) {
        messages.value[idx] = { ...messages.value[idx], streaming: false, content: '发送失败: ' + e.message, html: '' }
      }
    }
  }

  const handleChunk = (chunk: any) => {
    const chunkSid = typeof chunk === 'string' ? '' : String(chunk?.session_id || chunk?.sessionId || '')
    if (chunkSid && chunkSid !== sessionId.value && chunkSid !== activeStreamSession) return
    const text = typeof chunk === 'string' ? chunk : (chunk?.content || chunk?.text || chunk?.chunk || '')
    if (!text) return
    const idx = streamMsgIndex.value
    if (idx >= 0 && idx < messages.value.length && messages.value[idx]?.streaming) {
      const m = messages.value[idx]
      const newContent = (m.content || '') + text
      messages.value[idx] = { ...m, content: newContent, html: renderMarkdown(newContent), streaming: true }
      runStatusLine.value = '生成中…'
    }
  }

  const handleDone = () => {
    endStreamingUI()
    putCache(sessionId.value, messages.value)
    void loadSessions()
  }

  const handleError = (err: any) => {
    const msg = typeof err === 'string' ? err : (err?.error || err?.message || '未知错误')
    endStreamingUI('错误: ' + msg)
    const idx = streamMsgIndex.value
    if (idx >= 0 && messages.value[idx]?.streaming) {
      messages.value[idx] = { ...messages.value[idx], streaming: false, content: '错误: ' + msg, html: '' }
    }
  }

  const handleA2UI = (payload: any) => {
    const pSid = String(payload?.session_id || payload?.sessionId || '')
    if (pSid && pSid !== sessionId.value && pSid !== activeStreamSession) return
    const aui = makeAUIMessage(payload)
    aui._key = `${sessionId.value}_${chatPaneKey.value}_aui_${Date.now()}`
    if (aui.interactId) {
      activeInteractId.value = aui.interactId
      awaitingInteract.value = true
      sending.value = false
      runStatusLine.value = '等待交互…'
    }
    messages.value.push(aui)
    putCache(sessionId.value, messages.value)
  }

  const handleApprove = async (approvalId: string) => {
    try {
      const r = await wailsCall('ResolveApproval', approvalId, true, '')
      if (r?.success) {
        awaitingInteract.value = false
        sending.value = true
        runStatusLine.value = '已批准，Agent 继续处理…'
      } else { alert('批准失败: ' + (r?.error || '未知错误')) }
    } catch (e: any) { alert('批准异常: ' + e.message) }
  }

  const handleReject = async (approvalId: string) => {
    try {
      const r = await wailsCall('ResolveApproval', approvalId, false, '')
      if (r?.success) { awaitingInteract.value = false; runStatusLine.value = '已拒绝' }
      else alert('拒绝失败: ' + (r?.error || ''))
    } catch (e: any) { alert('拒绝异常: ' + e.message) }
  }

  const submitQuestion = async (msg: Message, answer: string, index: number) => {
    const interruptID = msg.interactId || msg.approval_id || ''
    if (!interruptID) {
      alert('缺少问题 interrupt_id')
      return
    }
    try {
      const r = await wailsCall('AnswerQuestion', interruptID, answer)
      if (r?.success) {
        msg.resolved = true
        awaitingInteract.value = false
        sending.value = false
        runStatusLine.value = ''
        if (r.content) {
          const sid = sessionId.value
          messages.value.push({
            role: 'assistant',
            type: 'text',
            content: r.content,
            html: renderMarkdown(r.content),
            _key: `${sid}_${chatPaneKey.value}_question_resume_${Date.now()}_${index}`,
          })
          putCache(sid, messages.value)
        }
        void loadSessions()
      } else {
        alert('提交失败: ' + (r?.error || ''))
      }
    } catch (e: any) { alert('提交异常: ' + e.message) }
  }

  const submitAUIForm = async (msg: Message, actionValue: string, index: number) => {
    const key = msg.interactId || String(index)
    const inputs = formInputs.value[key] || {}
    try {
      const r = await wailsCall('ResolveA2UIInteraction', msg.interactId || '', actionValue, JSON.stringify(inputs))
      if (r?.success) {
        msg.resolved = true
        if (activeInteractId.value === msg.interactId) activeInteractId.value = ''
        awaitingInteract.value = false
        runStatusLine.value = '已提交，Agent 继续处理…'
        sending.value = true
      } else alert('提交失败: ' + (r?.error || ''))
    } catch (e: any) { alert('提交异常: ' + e.message) }
  }

  const submitAUIAction = async (msg: Message, actionValue: string, index: number) => {
    try {
      const r = await wailsCall('ResolveA2UIInteraction', msg.interactId || '', actionValue, '{}')
      if (r?.success) {
        msg.resolved = true
        if (activeInteractId.value === msg.interactId) activeInteractId.value = ''
        awaitingInteract.value = false
        runStatusLine.value = '已提交，Agent 继续处理…'
        sending.value = true
      } else alert('提交失败: ' + (r?.error || ''))
    } catch (e: any) { alert('提交异常: ' + e.message) }
  }

  const reloadCurrentSession = async () => {
    const id = sessionId.value
    if (!id) return
    sessionLoading.value = true
    try {
      const resp = await wailsCallWithTimeout('GetSessionMessages', 8000, id)
      const next = parseResp(resp, id)
      applyMessages(id, next)
    } catch {}
    finally { sessionLoading.value = false }
  }

  const setupWailsEvents = async () => {
    const Ev = await waitForWailsEvents(3000)
    if (!Ev?.On) { console.warn('[AgentGo] Wails Events 未就绪（非桌面环境）'); return }
    cleanup()
    unsubs = []

    const sub = (ev: string, cb: (...a: any[]) => void) => {
      const u = Ev.On(ev, (e: any) => {
        const payload = e && typeof e === 'object' && e.name === ev && 'data' in e ? e.data : e
        cb(payload)
      })
      if (typeof u === 'function') unsubs.push(u)
    }

    // ── Core chat events (actual bridge event names) ──────────────────────
    sub('chat:chunk', (payload: any) => {
      const sid = String(payload?.session_id || payload?.sessionId || '')
      if (sid && sid !== sessionId.value && sid !== activeStreamSession) return
      const text = String(payload?.delta || payload?.content || payload?.text || '')
      handleChunk({ session_id: sid, content: text })
    })

    sub('chat:done', (payload: any) => {
      const sid = String(payload?.session_id || payload?.sessionId || '')
      if (sid && sid !== sessionId.value && sid !== activeStreamSession) return
      if (payload?.error) handleError(payload.error)
      else {
        if (payload?.resume && Array.isArray(payload?.messages)) {
          appendServerMessages(payload.messages, sid || sessionId.value)
        }
        handleDone()
      }
    })

    sub('a2ui:render', handleA2UI)

    sub('approval:pending', (payload: any) => {
      const sid = String(payload?.session_id || payload?.sessionId || '')
      if (sid && sid !== sessionId.value && sid !== activeStreamSession) return
      awaitingInteract.value = true
      sending.value = false
      runStatusLine.value = '等待批准…'
      const idx = streamMsgIndex.value
      if (idx >= 0 && messages.value[idx]?.streaming) {
        messages.value[idx] = {
          ...messages.value[idx], streaming: false, type: 'approval',
          approval_id: payload?.approval_id || payload?.id || '',
          content: payload?.prompt || '需要您的批准才能继续',
        }
        streamMsgIndex.value = -1
      }
    })

    sub('ask:pending', (payload: any) => {
      const q = payload?.question
      const questionText = typeof q === 'string' ? q
        : (q?.text || q?.question || q?.prompt || '请回答：')
      awaitingInteract.value = true
      sending.value = false
      runStatusLine.value = '等待输入…'
      const idx = streamMsgIndex.value
      const nextQuestion: Message = {
        role: 'assistant', type: 'question',
        content: questionText,
        data: typeof q === 'object' && q ? q : {},
        interactId: payload?.interrupt_id || payload?.interact_id || '',
        approval_id: payload?.approval_id || payload?.interrupt_id || '',
        _key: `${sessionId.value}_${chatPaneKey.value}_question_${Date.now()}`,
      }
      if (idx >= 0 && messages.value[idx]?.streaming) {
        messages.value[idx] = {
          ...messages.value[idx],
          ...nextQuestion,
          streaming: false,
        }
        streamMsgIndex.value = -1
      } else {
        messages.value.push(nextQuestion)
      }
    })

    sub('chat:paused', (payload: any) => {
      awaitingInteract.value = true
      sending.value = false
      if (!runStatusLine.value || runStatusLine.value === '生成中…' || runStatusLine.value === '思考中…') {
        runStatusLine.value = '等待交互…'
      }
    })

    // ── Agent trace events — used for tool call status display ─────────────
    sub('agent:trace', (payload: any) => {
      if (!sending.value) return
      const component = String(payload?.component || '')
      const phase = String(payload?.phase || '')
      const name = String(payload?.name || payload?.detail || '')
      if (component === 'Tool') {
        if (phase === 'start') {
          runStatusLine.value = `🔧 调用 ${name}…`
        } else if (phase === 'end') {
          runStatusLine.value = '生成中…'
        } else if (phase === 'error') {
          runStatusLine.value = `⚠️ ${name} 出错`
        }
      } else if (component === 'Runner' || component === 'Memory') {
        if (phase === 'start' && name) {
          runStatusLine.value = name === 'recall' ? '检索记忆…' : '思考中…'
        }
      }
    })

    cleanupFn = () => {
      unsubs.forEach(u => u())
      unsubs = []
    }
    console.info('[AgentGo] Wails 事件已绑定', unsubs.length, '个')
  }

  const cleanup = () => { cleanupFn?.(); cleanupFn = null }

  return {
    messages, sessions, sessionId, sessionLoading, sending,
    awaitingInteract, runStatusLine, chatPaneKey, streamMsgIndex, formInputs,
    loadSessions, newSession, selectSession, stopGeneration, sendMessage,
    handleApprove, handleReject, submitQuestion, submitAUIForm, submitAUIAction,
    reloadCurrentSession, setupWailsEvents, cleanup,
  }
}
