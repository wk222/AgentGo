import { ref, reactive, computed, shallowRef, nextTick } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { RuntimeClient, type RuntimeEvent } from '../shared/runtime-client'
import {
  wailsCall,
  wailsCallRaw,
  wailsCallUrgent,
  wailsCallWithTimeout,
  wailsEvents,
  waitForWailsEvents,
  resetIpcQueue,
} from '../shared/wails'
import { notify } from '../shared/ui/notify'
import { forkNotice, forkedId } from './forkModel'
import { checkpointWarning, rowMeta, tagStartedCheckpoint } from './turnCheckpointModel'
import { projectToolToStage } from './agentBridge'
import {
  createRun, runBadge, sameConversation, isPlaceholderMessage,
  type SessionRun, type RunBadge,
} from './chatRun'

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
  /** 分叉出来的对话才有，见 features/chat/forkModel。 */
  parent_id?: string
  parent_title?: string
  parent_gone?: boolean
}

export interface Message {
  role: 'user' | 'assistant' | 'system'
  type: 'text' | 'aui' | 'approval' | 'tool_call' | 'question' | 'reasoning'
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
  if (/^workflow_(?:wf_)?[a-f0-9]+$/i.test(t)) return `工作流会话 · ${t.slice(-6)}`
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

/** 续跑（批准 / 回答）返回后，等多久还没收到 done 事件就用返回的消息收尾。 */
const RESUME_SETTLE_MS = 2500

export function useChat() {
  const sessions = ref<Session[]>([])
  const sessionId = ref('')
  const sessionLoading = ref(false)
  const chatPaneKey = ref(0)
  const formInputs = ref<Record<string, Record<string, any>>>({})

  // 每个会话一份"对话 + 运行"状态（见 chatRun.ts）。cur 指向当前显示的那份；
  // 切换会话只是换指针，后台会话的运行照常接收事件、照常累积消息。
  const runs = reactive(new Map<string, SessionRun>())
  const cur = shallowRef<SessionRun>(createRun(''))
  const field = <K extends keyof SessionRun>(key: K) => computed<SessionRun[K]>({
    get: () => cur.value[key],
    set: v => { cur.value[key] = v },
  })
  const messages = field('msgs')
  const sending = field('sending')
  const awaitingInteract = field('awaiting')
  const runStatusLine = field('status')
  const streamMsgIndex = field('streamIdx')

  /** 各会话的运行标记：给会话列表显示"正在运行 / 等待批准"。 */
  const runBadges = computed(() => {
    const out: Record<string, RunBadge> = {}
    for (const [sid, r] of runs) {
      const b = runBadge(r)
      if (b) out[sid] = b
    }
    return out
  })

  let sessionLoadSeq = 0
  let unsubs: Array<() => void> = []
  let cleanupFn: (() => void) | null = null

  const runtime = new RuntimeClient({
    call: (method, ...args) => wailsCallRaw(method, ...args),
    on: (name, listener) => wailsEvents()!.On(name, listener),
  })

  const bumpPane = () => { chatPaneKey.value++ }

  const runOf = (id: string): SessionRun => {
    let r = runs.get(id)
    if (!r) {
      r = createRun(id)
      runs.set(id, r)
    }
    return r
  }

  /** 切到某个会话（只换指针，不动运行）。空 id 表示"还没有会话"。 */
  const enter = (id: string) => {
    cur.value = id ? runOf(id) : createRun('')
    sessionId.value = id
  }

  /** 首条消息发送前还没有会话：建好后把这份状态挂到新会话上。 */
  const adopt = (st: SessionRun, sid: string) => {
    st.sid = sid
    runs.set(sid, st)
    if (cur.value === st) sessionId.value = sid
  }

  const isBusy = (r: SessionRun) => r.sending || r.awaiting || r.starting

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

  const stampKeys = (msgs: Message[], sid: string, offset = 0) =>
    (msgs || []).map((m, i) => ({
      ...m,
      _key: m._key || `${sid}_${chatPaneKey.value}_${offset + i}_${m.role}`,
    }))

  /** 用服务端的历史整体替换某个会话的消息。 */
  const applyMessages = (id: string, msgs: Message[]) => {
    const st = runs.get(id)
    if (!st) return
    st.msgs = stampKeys(deepClone(msgs), id)
    st.loaded = true
  }

  const endStreamingUI = (st: SessionRun, msg?: string) => {
    st.sending = false
    st.awaiting = false
    st.streamIdx = -1
    st.reasoningIdx = -1
    st.status = msg || ''
    for (const m of st.msgs) {
      if (m.streaming) {
        m.streaming = false
        if (m.type !== 'reasoning' && (m.content || '').trim()) m.html = renderMarkdown(m.content!)
      }
    }
  }

  const parseQuestionData = (rawData: any, rawArgs?: string): Record<string, any> => {
    const out: Record<string, any> = (rawData && typeof rawData === 'object') ? { ...rawData } : {}
    if (rawArgs && typeof rawArgs === 'string' && rawArgs.trim()) {
      try {
        const parsed = JSON.parse(rawArgs)
        if (parsed && typeof parsed === 'object') {
          Object.assign(out, parsed)
        }
      } catch {}
    }
    if (typeof out.choices === 'string' && out.choices.trim()) {
      try {
        const arr = JSON.parse(out.choices)
        if (Array.isArray(arr)) out.choices = arr
      } catch {
        out.choices = out.choices.split(/[,，\n]/).map((s: string) => s.trim()).filter(Boolean)
      }
    }
    return out
  }

  const mapRow = (m: any): Message => {
    const t = String(m.type || m.msg_type || '').toLowerCase()
    if (t === 'source_evidence') {
      return { role: 'assistant', type: 'tool_call', toolName: 'read_evidence', status: 'success', content: String(m.content || '') }
    }
    if (t === 'artifact') {
      // 交付记录是不进模型历史的非文本行；还原成和 deliver_file 工具结果一样的卡片
      let rec: Record<string, unknown> = {}
      try { rec = JSON.parse(String(m.content || '{}')) } catch { rec = {} }
      return { role: 'assistant', type: 'tool_call', toolName: 'deliver_file', status: 'success', content: JSON.stringify({ success: true, ...rec }) }
    }
    if (t === 'aui' || m.component || m.data_json || m.dataJson) {
      return makeAUIMessage({ ...m, type: 'aui' })
    }
    const msgType = m.type || 'text'
    const qData = msgType === 'question' ? parseQuestionData(m.data, m.arguments) : m.data
    const rawContent = m.content || (msgType === 'question' && qData?.prompt ? String(qData.prompt) : '')
    const content = stripToolLeak(rawContent)
    const id = m.approval_id || m.interact_id || m.interrupt_id || ''
    return {
      role: m.role,
      type: msgType,
      content,
      html: msgType === 'text' ? renderMarkdown(content) : '',
      approval_id: id || m.approval_id,
      interactId: m.interact_id || m.interrupt_id || m.approval_id,
      data: qData,
      toolName: m.tool_name,
      arguments: m.arguments,
      status: m.status,
      resolved: Boolean(m.resolved || ((msgType === 'approval' || msgType === 'question') && m.status && m.status !== 'pending')),
      meta: rowMeta(m),
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

  const appendServerMessages = (st: SessionRun, rows: any[], sid: string) => {
    const mapped = rowsToMessages(rows || [], sid)
      .filter(m => !isPlaceholderMessage(m))
      .filter(m => m.type !== 'text' || !!(m.content || '').trim())
    if (!mapped.length) return
    st.msgs.push(...stampKeys(mapped, sid, st.msgs.length))
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

  /** 新建会话并切过去。正在跑的会话不受影响，继续在后台运行。 */
  const newSession = async () => {
    sessionLoadSeq++
    try {
      const r = await wailsCall('NewSession', '新对话')
      if (r?.success && (r.session || r.id)) {
        const sid = sessionIdOf(r.session) || String(r.id || '').trim()
        if (!sid) throw new Error('NewSession 未返回 id')
        bumpPane()
        const st = runOf(sid)
        st.msgs = welcomeMsg()
        st.loaded = true
        enter(sid)
        sessionLoading.value = false
        localStorage.setItem('agentgo-last-session', sid)
        await loadSessions()
      }
    } catch (e: any) { console.error('[AgentGo] newSession:', e?.message) }
  }

  /** 删除前先停掉这个会话还在进行的运行，并丢掉它的内存状态。 */
  const dropRun = async (id: string) => {
    const st = runs.get(id)
    if (!st) return
    if (isBusy(st)) await stopRun(st)
    runs.delete(id)
  }

  const leaveCurrent = () => {
    cur.value = createRun('')
    sessionId.value = ''
    localStorage.removeItem('agentgo-last-session')
  }

  // deleteSession removes a history entry; deleting the active one starts a fresh chat.
  const deleteSession = async (id: string) => {
    id = String(id || '').trim()
    if (!id) return
    const wasActive = sessionId.value === id
    await dropRun(id)
    try { await wailsCall('DeleteSession', id) } catch (e: any) {
      console.error('[AgentGo] deleteSession:', e?.message)
      return
    }
    if (wasActive) {
      leaveCurrent()
      await newSession()
    } else {
      await loadSessions()
    }
  }

  // renameSession 给历史对话改名。返回实际保存的标题（后端会压成单行、最多 80 字）；失败时抛出带原因的错误。
  const renameSession = async (id: string, title: string): Promise<string> => {
    const r = await wailsCall('RenameSession', String(id || '').trim(), title)
    if (!r?.success) throw new Error(String(r?.error || '重命名失败'))
    await loadSessions()
    return String(r.title || title)
  }

  const forking = ref(false)

  /**
   * 把一条对话整条分叉成新对话并切过去；原对话不动，之后两边各走各的（仍共用工作区文件）。
   * 原对话正在运行或停在审批上时后端会拒绝：只提示，不当错误。
   * 返回新对话的 id；没分叉成功返回空串（原因已经用提示告诉用户）。
   */
  const forkSession = async (id?: string): Promise<string> => {
    const src = String(id || sessionId.value || '').trim()
    if (!src || forking.value) return ''
    forking.value = true
    try {
      const known = sessions.value.find(s => sessionIdOf(s) === src)
      let r: any
      try { r = await wailsCall('ForkSession', src, '') } catch (e: any) { r = { success: false, error: String(e?.message || e) } }
      const sid = r?.success ? forkedId(r) : ''
      if (r?.success && !sid) { notify.error('分叉没有返回新对话的编号，请刷新后在列表里找它'); await loadSessions(); return '' }
      const note = forkNotice(r, known ? sessionTitle(known) : '')
      notify[note.tone](note.text)
      if (!sid) return ''
      await loadSessions()
      await selectSession(sid)
      return sid
    } finally { forking.value = false }
  }

  // deleteSessionsBatch removes multiple history entries in one shot.
  const deleteSessionsBatch = async (ids: string[]) => {
    const cleanIds = Array.from(new Set((ids || []).map(x => String(x || '').trim()).filter(Boolean)))
    if (!cleanIds.length) return
    const activeDeleted = cleanIds.includes(sessionId.value)
    for (const id of cleanIds) await dropRun(id)
    try {
      await wailsCall('DeleteSessionsBatch', cleanIds)
    } catch {
      for (const id of cleanIds) {
        try { await wailsCall('DeleteSession', id) } catch {}
      }
    }
    if (activeDeleted) {
      leaveCurrent()
      await newSession()
    } else {
      await loadSessions()
    }
  }

  // deleteOtherSessions wipes ALL sessions in SQLite except keepId (not limited to top 100).
  const deleteOtherSessions = async (keepId?: string): Promise<number> => {
    let targetKeep = String(keepId || sessionId.value || '').trim()
    if (!targetKeep && sessions.value.length > 0) {
      targetKeep = sessionIdOf(sessions.value[0])
    }
    let deleted = 0
    try {
      const r = await wailsCall('DeleteOtherSessions', targetKeep)
      deleted = Number(r?.deleted || 0)
    } catch (e: any) {
      console.error('[AgentGo] deleteOtherSessions:', e?.message)
    }
    for (const k of Array.from(runs.keys())) {
      if (k !== targetKeep) await dropRun(k)
    }
    await loadSessions()
    if (targetKeep && sessionId.value !== targetKeep) {
      await selectSession(targetKeep)
    } else if (!targetKeep && sessions.value.length === 0) {
      await newSession()
    }
    return deleted
  }

  // deleteSessionsByTitle wipes ALL sessions with matching title across the entire SQLite DB.
  const deleteSessionsByTitle = async (title: string, keepCurrent = true): Promise<number> => {
    const cleanTitle = String(title || '').trim()
    if (!cleanTitle) return 0
    const keepId = keepCurrent ? sessionId.value : ''
    let deleted = 0
    try {
      const r = await wailsCall('DeleteSessionsByTitle', cleanTitle, keepId)
      deleted = Number(r?.deleted || 0)
    } catch (e: any) {
      console.error('[AgentGo] deleteSessionsByTitle:', e?.message)
    }
    await loadSessions()
    const alive = new Set(sessions.value.map(sessionIdOf))
    for (const [k, r] of Array.from(runs.entries())) {
      if (!alive.has(k) && k !== sessionId.value && !isBusy(r)) runs.delete(k)
    }
    if (!sessions.value.some(s => sessionIdOf(s) === sessionId.value)) {
      if (sessions.value.length > 0) await selectSession(sessionIdOf(sessions.value[0]))
      else await newSession()
    }
    return deleted
  }

  // clearSession = Copilot "Clear": drop the current conversation and start an empty one.
  const clearSession = async () => {
    const id = sessionId.value
    if (id) await deleteSession(id)
    else await newSession()
  }

  /**
   * 切换到某个会话。不会取消任何运行：
   * - 目标会话正在后台跑：直接显示内存里的状态（流式文字、工具气泡、等待批准都在）；
   * - 目标会话在内存里有记录：先显示，再用服务端历史校对，对不上才替换；
   * - 否则从服务端加载。
   */
  const selectSession = async (id: string) => {
    id = String(id || '').trim()
    if (!id) return
    const st = runOf(id)
    const switching = sessionId.value !== id
    const live = st.sending || st.starting
    if (!switching && live) return

    sessionLoadSeq++
    const mySeq = sessionLoadSeq
    bumpPane()
    enter(id)
    localStorage.setItem('agentgo-last-session', id)

    if (live) {
      sessionLoading.value = false
      return
    }

    const hadMemory = st.loaded && messagesHaveRealContent(st.msgs)
    sessionLoading.value = !hadMemory && sessionMsgCount(id) > 0
    if (!hadMemory) st.msgs = []

    try {
      await nextTick()
      const resp = await wailsCallWithTimeout('GetSessionMessages', 8000, id)
      if (mySeq !== sessionLoadSeq || sessionId.value !== id) return
      if (st.sending || st.starting) return // 加载期间用户已在这个会话里发了消息
      const next = parseResp(resp, id)
      if (hadMemory && sameConversation(st.msgs, next)) { st.loaded = true; return }
      if (next.length) applyMessages(id, next)
      else if (!messagesHaveRealContent(st.msgs)) applyMessages(id, welcomeMsg())
    } catch (e: any) {
      if (mySeq !== sessionLoadSeq || sessionId.value !== id) return
      if (hadMemory) return
      applyMessages(id, [{ role: 'assistant', type: 'text', content: '加载失败: ' + e.message }])
    } finally {
      if (mySeq === sessionLoadSeq && sessionId.value === id) sessionLoading.value = false
    }
  }

  const stopRun = async (st: SessionRun) => {
    st.sendSeq++
    st.starting = false
    st.startEvents = []
    const sid = st.sid
    st.ignoredRunID = st.runID
    endStreamingUI(st, '已停止')
    resetIpcQueue()
    const interactID = st.interactId
    st.interactId = ''
    if (interactID) { try { await wailsCallUrgent('CancelA2UIInteraction', 1500, interactID) } catch {} }
    if (sid) {
      try { await wailsCallUrgent('CancelEngineSession', 3000, sid) } catch {}
      // Checkpoint continuations are owned by Runner rather than Router.
      try { await wailsCallUrgent('StopSession', 3000, sid) } catch {}
    }
  }

  const stopGeneration = () => stopRun(cur.value)

  /** 按会话 id 停止（「运行中」视图用）。本页发起、还在忙的走完整的停止流程；
   *  其余的（页面刷新前就在跑的）本页没有它的状态，直接通知后端。 */
  const stopSessionById = async (sid: string) => {
    const st = runs.get(sid)
    if (st && isBusy(st)) return stopRun(st)
    try { await wailsCallUrgent('CancelEngineSession', 3000, sid) } catch {}
    try { await wailsCallUrgent('StopSession', 3000, sid) } catch {}
  }

  const sendMessage = async (text: string, images?: string[], beforeStart?: (sessionID: string) => Promise<void>) => {
    text = String(text || '').trim()
    const st = cur.value
    if (!text || st.sending) return
    const mySend = ++st.sendSeq
    const live = () => mySend === st.sendSeq
    if (st.awaiting) {
      st.awaiting = false
      st.msgs.forEach(m => {
        if ((m.type === 'approval' || m.type === 'question' || m.type === 'aui') && !m.resolved) {
          m.resolved = true
        }
      })
    }

    st.runID = ''
    st.reasoningIdx = -1
    st.segStart = st.msgs.length

    st.msgs.push({
      role: 'user', type: 'text', content: text, html: '',
      meta: images && images.length > 0 ? { images } : undefined,
      _key: `${st.sid}_${chatPaneKey.value}_user_${Date.now()}`,
    })

    const streamMsg: Message = {
      role: 'assistant', type: 'text', content: '', html: '', streaming: true,
      _key: `${st.sid}_${chatPaneKey.value}_stream_${Date.now()}`,
    }
    st.msgs.push(streamMsg)
    st.streamIdx = st.msgs.length - 1

    st.sending = true
    st.status = '消息已发送，准备会话…'
    sessionLoading.value = false
    sessionLoadSeq++ // Prevent a pending history load from replacing the optimistic turn.
    resetIpcQueue()

    try {
      await nextTick()
      if (!live()) return
      if (!st.sid) {
        const r = await wailsCall('NewSession', '新对话')
        const created = sessionIdOf(r?.session) || String(r?.id || '').trim()
        if (!r?.success || !created) throw new Error('无法创建会话')
        if (!live()) return
        adopt(st, created)
        localStorage.setItem('agentgo-last-session', created)
        void loadSessions()
      }
      const sid = st.sid
      if (!st.sending || !live()) return
      if (beforeStart) {
        st.status = '准备工作区保护…'
        await beforeStart(sid)
      }
      if (!st.sending || !live()) return
      st.status = '正在连接模型…'
      st.starting = true
      st.startEvents = []
      await setupWailsEvents()
      if (!live()) return
      const startedRunID = await runtime.start(sid, text, images || [])
      if (!live()) {
        try { await wailsCallUrgent('CancelEngineRun', 3000, startedRunID) } catch {}
        return
      }
      st.runID = startedRunID
      st.starting = false
      const queued = st.startEvents
      st.startEvents = []
      queued.forEach(ev => applyRuntimeEvent(st, ev))
    } catch (e: any) {
      if (!live()) return
      st.starting = false
      st.startEvents = []
      handleError(st, '发送失败: ' + e.message)
    }
  }

  const handleChunk = (st: SessionRun, text: string) => {
    if (!text) return
    const idx = st.streamIdx
    if (idx >= 0 && idx < st.msgs.length && st.msgs[idx]?.streaming) {
      const m = st.msgs[idx]
      const newContent = (m.content || '') + text
      st.msgs[idx] = { ...m, content: newContent, html: renderMarkdown(newContent), streaming: true }
      st.status = '生成中…'
    }
  }

  const handleDone = (st: SessionRun) => {
    endStreamingUI(st)
    void loadSessions()
  }

  const handleError = (st: SessionRun, err: any) => {
    const msg = typeof err === 'string' ? err : (err?.error || err?.message || '未知错误')
    const idx = st.streamIdx
    endStreamingUI(st, '错误: ' + msg)
    if (idx >= 0 && st.msgs[idx]) {
      const content = [st.msgs[idx].content, '错误: ' + msg].filter(Boolean).join('\n\n')
      st.msgs[idx] = { ...st.msgs[idx], streaming: false, content, html: renderMarkdown(content) }
    } else if (st.msgs[st.msgs.length - 1]?.content !== '错误: ' + msg) {
      st.msgs.push({ role: 'assistant', type: 'text', content: '错误: ' + msg, html: renderMarkdown('错误: ' + msg) })
    }
  }

  const handleA2UI = (payload: any) => {
    const st = routeLegacy(payload)
    if (!st) return
    const aui = makeAUIMessage(payload)
    aui._key = `${st.sid}_${chatPaneKey.value}_aui_${Date.now()}`
    if (aui.interactId) {
      st.interactId = aui.interactId
      st.awaiting = true
      st.sending = false
      st.status = '等待交互…'
    }
    st.msgs.push(aui)
  }

  /**
   * 续跑返回后的兜底：续跑已在服务端结束，正常情况下 done 事件早已把界面收尾。
   * 万一事件没送到（连接断过），用返回的消息补上，免得界面一直停在"继续处理"。
   */
  const settleAfterResume = async (st: SessionRun, result: any, seq: number) => {
    await new Promise(res => setTimeout(res, RESUME_SETTLE_MS))
    // 等待期间用户又发了新消息 / 点了停止：现在的运行不是这次续跑，不能替它收尾。
    if (!st.sending || st.sendSeq !== seq) return
    const msgs: any[] = Array.isArray(result?.messages) ? result.messages : []
    const streamed = st.msgs.slice(st.segStart).some(m => m.role === 'assistant' && m.type === 'text' && m.content)
    const rows = msgs.filter(m => {
      if (m.type === 'approval' || m.type === 'question') return !st.msgs.some(x => x.approval_id === m.approval_id)
      return !streamed
    })
    appendServerMessages(st, rows, st.sid)
    handleDone(st)
    if (msgs.some(m => (m.type === 'approval' || m.type === 'question') && (!m.status || m.status === 'pending'))) {
      st.awaiting = true
      st.status = '等待交互…'
    }
  }

  const handleApprove = async (approvalId: string, note = '', overrideArgs = '', remember: '' | 'call' | 'tool' = '') => {
    const st = cur.value
    const seq = st.sendSeq
    st.awaiting = false
    st.sending = true
    st.status = overrideArgs ? '已改参批准，Agent 继续处理…' : '已批准，Agent 继续处理…'
    st.msgs.filter(m => !approvalId || m.approval_id === approvalId).forEach(m => {
      if (m.type === 'approval') {
        m.resolved = true
        m.status = 'approved'
        if (overrideArgs) m.arguments = overrideArgs
      }
    })
    if (!approvalId) {
      st.sending = false
      st.status = '已标记为已处理'
      return
    }
    try {
      const r = remember && !overrideArgs
        ? await wailsCall('ApproveAndRemember', approvalId, remember, note)
        : await wailsCall('ResolveApproval', approvalId, true, note, overrideArgs)
      if (!r?.success || r?.resume_error) {
        st.sending = false
        st.awaiting = false
        const errMsg = String(r?.resume_error || r?.error || '')
        if (errMsg.includes('没有可恢复的运行') || errMsg.includes('not found') || errMsg.includes('already resolved')) {
          st.status = '历史审批已归档'
          notify.info('该历史审批所属会话已结束，已为您自动归档')
        } else {
          handleError(st, errMsg || '批准失败')
        }
      } else {
        void settleAfterResume(st, r, seq)
      }
    } catch (e: any) {
      st.sending = false
      st.awaiting = false
      handleError(st, e.message)
    }
  }

  const handleReject = async (approvalId: string, note = '') => {
    const st = cur.value
    st.msgs.filter(m => !approvalId || m.approval_id === approvalId).forEach(m => {
      if (m.type === 'approval') {
        m.resolved = true
        m.status = 'rejected'
      }
    })
    st.awaiting = false
    st.sending = false
    st.status = note ? '已拒绝并附修改意见' : '已拒绝'
    if (!approvalId) return
    try {
      await wailsCall('ResolveApproval', approvalId, false, note, '')
    } catch {}
  }

  const steerSession = async (instruction: string) => {
    const text = instruction.trim()
    const st = cur.value
    if (!text || !st.sid) return false
    try {
      const res = await wailsCall('SteerSession', st.sid, text)
      if (res?.success !== false) {
        st.msgs.push({
          role: 'user',
          type: 'text',
          content: `⚡ [中途纠偏 Steer] ${text}`,
          _key: `${st.sid}_${chatPaneKey.value}_steer_${Date.now()}`,
        })
        st.status = '⚡ 已注入纠偏指令，Agent 正在调整…'
        notify.info('已向运行中的 Agent 注入实时纠偏指令')
        return true
      }
      notify.error(res?.error || '纠偏指令发送失败')
      return false
    } catch (e: any) {
      notify.error('纠偏异常: ' + (e?.message || e))
      return false
    }
  }

  const submitQuestion = async (msg: Message, answer: string, index: number) => {
    const cleanAns = String(answer || '').trim()
    if (!cleanAns) return
    const st = cur.value
    const interruptID = msg.interactId || msg.approval_id || ''
    msg.resolved = true
    st.msgs.forEach(m => {
      if (m.type === 'question' && (!interruptID || m.interactId === interruptID || m.approval_id === interruptID)) {
        m.resolved = true
      }
    })
    st.awaiting = false

    if (!interruptID) {
      st.sending = false
      st.status = '已提交选择…'
      await sendMessage(cleanAns)
      return
    }
    // 回答会把暂停的运行续跑到结束才返回，期间界面保持"运行中"。
    st.sending = true
    st.status = '已提交，Agent 继续处理…'
    try {
      const r = await wailsCall('AnswerQuestion', interruptID, cleanAns)
      if (r?.success) {
        st.sending = false
        st.status = ''
        if (r.content) {
          st.msgs.push({
            role: 'assistant',
            type: 'text',
            content: r.content,
            html: renderMarkdown(r.content),
            _key: `${st.sid}_${chatPaneKey.value}_question_resume_${Date.now()}_${index}`,
          })
        }
        // 续跑又停在了新的审批 / 提问上：把它显示出来，否则这一轮就没有后续了。
        if (r.message && typeof r.message === 'object') {
          const next = mapRow({ ...(r.message as Record<string, any>), session_id: st.sid })
          const dup = next.approval_id && st.msgs.some(m => m.approval_id === next.approval_id)
          if (!dup) {
            next._key = `${st.sid}_${chatPaneKey.value}_resume_pause_${Date.now()}`
            st.msgs.push(next)
          }
          if (!next.resolved) {
            st.awaiting = true
            st.status = next.type === 'question' ? '等待输入…' : '等待批准…'
          }
        }
        void loadSessions()
      } else {
        // 历史问题或检查点已过期：自动将所选选项作为新消息发送，保证选项点击 100% 可用
        st.sending = false
        await sendMessage(cleanAns)
      }
    } catch {
      st.sending = false
      await sendMessage(cleanAns)
    }
  }

  const submitAUIForm = async (msg: Message, actionValue: string, index: number) => {
    const st = cur.value
    const key = msg.interactId || String(index)
    const inputs = formInputs.value[key] || {}
    msg.resolved = true
    if (st.interactId === msg.interactId) st.interactId = ''
    st.awaiting = false
    try {
      const r = await wailsCall('ResolveA2UIInteraction', msg.interactId || '', actionValue, JSON.stringify(inputs))
      if (r?.success) {
        st.status = '已提交，Agent 继续处理…'
        st.sending = true
      } else {
        const summary = Object.keys(inputs).length ? `${actionValue}: ${JSON.stringify(inputs)}` : actionValue
        await sendMessage(summary)
      }
    } catch {
      const summary = Object.keys(inputs).length ? `${actionValue}: ${JSON.stringify(inputs)}` : actionValue
      await sendMessage(summary)
    }
  }

  const submitAUIAction = async (msg: Message, actionValue: string, _index: number) => {
    const st = cur.value
    msg.resolved = true
    if (st.interactId === msg.interactId) st.interactId = ''
    st.awaiting = false
    try {
      const r = await wailsCall('ResolveA2UIInteraction', msg.interactId || '', actionValue, '{}')
      if (r?.success) {
        st.status = '已提交，Agent 继续处理…'
        st.sending = true
      } else if (actionValue) {
        await sendMessage(actionValue)
      }
    } catch {
      if (actionValue) await sendMessage(actionValue)
    }
  }

  const reloadCurrentSession = async () => {
    const st = cur.value
    const id = st.sid
    if (!id || st.sending || st.starting) return // 运行中的流式内容还没落库，重新加载会把它冲掉
    sessionLoading.value = true
    try {
      const resp = await wailsCallWithTimeout('GetSessionMessages', 8000, id)
      if (st.sending || st.starting) return
      applyMessages(id, parseResp(resp, id))
    } catch {}
    finally { sessionLoading.value = false }
  }

  /** 把一段思考文字追加到当前这一段的"思考"消息里（没有就新建一条）。 */
  const appendReasoning = (st: SessionRun, event: RuntimeEvent, delta: string) => {
    st.status = '思考中…'
    if (!delta) return
    let idx = st.reasoningIdx
    if (idx < 0 || st.msgs[idx]?.type !== 'reasoning') {
      const item: Message = {
        role: 'assistant', type: 'reasoning', content: '', streaming: true,
        _key: `${event.run_id}_reasoning_${event.seq}`,
      }
      // 文字还没开始输出时，占位的"正在处理"气泡在最后：思考块要排在它前面。
      const open = st.streamIdx >= 0 ? st.msgs[st.streamIdx] : undefined
      if (open?.streaming && !(open.content || '').trim()) {
        idx = st.streamIdx
        st.msgs.splice(idx, 0, item)
        st.streamIdx = idx + 1
      } else {
        st.msgs.push(item)
        idx = st.msgs.length - 1
      }
      st.reasoningIdx = idx
    }
    const m = st.msgs[idx]
    m.content = (m.content || '') + delta
    // 正文已经在输出时迟到的思考片段只追加，不重新亮起"思考中"。
    if (st.streamIdx < 0 || !(st.msgs[st.streamIdx]?.content || '').trim()) m.streaming = true
  }

  const closeReasoning = (st: SessionRun) => {
    const m = st.reasoningIdx >= 0 ? st.msgs[st.reasoningIdx] : undefined
    if (m?.type === 'reasoning') m.streaming = false
  }

  // 建回退点失败不影响这一轮；同一个原因只提醒一次，免得每轮都弹。
  let lastCheckpointFailure = ''
  const notifyCheckpointFailure = (error: string) => {
    if (error === lastCheckpointFailure) return
    lastCheckpointFailure = error
    notify.warning(checkpointWarning(error))
  }

  const applyRuntimeEvent = (st: SessionRun, event: RuntimeEvent) => {
    if (event.run_id === st.ignoredRunID) return
    if (st.starting) { st.startEvents.push(event); return }
    if (st.runID && event.run_id !== st.runID) return
    st.runID = event.run_id
    const foreground = st === cur.value
    const p = event.payload || {}
    if (event.type === 'status') {
      if (p.state === 'resumed') { st.segStart = st.msgs.length; st.reasoningIdx = -1 }
      if (p.state === 'started' || p.state === 'resumed') {
        // 本页发起的运行在 sendMessage 里就已经是 sending；别处发起的（IM、自动化）要到这里才变忙。
        const initiatedHere = st.sending
        st.sending = true
        st.awaiting = false
        st.status = p.state === 'resumed' ? '继续处理…' : '思考中…'
        if (p.state === 'started' && tagStartedCheckpoint(st.msgs, st.segStart, p, initiatedHere) === 'failed') {
          notifyCheckpointFailure(String(p.checkpoint_error))
        }
      }
    } else if (event.type === 'token') {
      closeReasoning(st)
      if (st.streamIdx < 0 || !st.msgs[st.streamIdx]?.streaming) {
        st.msgs.push({ role: 'assistant', type: 'text', content: '', streaming: true,
          _key: `${event.run_id}_${event.seq}_text` })
        st.streamIdx = st.msgs.length - 1
      }
      handleChunk(st, String(p.delta ?? ''))
    } else if (event.type === 'reasoning') {
      appendReasoning(st, event, String(p.delta ?? ''))
    } else if (event.type === 'tool_call') {
      closeReasoning(st)
      st.reasoningIdx = -1
      const idx = st.streamIdx
      if (idx >= 0 && st.msgs[idx]) st.msgs[idx].streaming = false
      st.streamIdx = -1
      st.msgs.push({ role: 'assistant', type: 'tool_call', toolName: p.name,
        arguments: p.arguments, status: 'running', meta: { runID: event.run_id, callID: p.id },
        _key: `${event.run_id}_tool_${p.id}_${event.seq}` })
      st.status = `调用 ${p.name}…`
      if (foreground) projectToolToStage(p.name, p.arguments, false)
    } else if (event.type === 'tool_result') {
      const item = [...st.msgs].reverse().find(m => m.type === 'tool_call' && m.meta?.runID === event.run_id && m.meta?.callID === p.id)
      if (item) {
        item.content = p.output
        item.status = p.is_error ? 'error' : 'success'
        if (!p.is_error && foreground) projectToolToStage(item.toolName, item.arguments, true)
      }
    } else if (event.type === 'approval_request') {
      endStreamingUI(st)
      st.awaiting = true
      const isQ = p.tool_name === 'ask_user'
      st.status = isQ ? '等待输入…' : '等待批准…'
      const qData = isQ ? parseQuestionData(undefined, p.arguments) : undefined
      const qContent = isQ ? (qData?.prompt || qData?.question || p.prompt || '请回答：') : p.prompt
      const existing = st.msgs.find(m => m.approval_id === p.approval_id || (isQ && m.type === 'question' && !m.resolved))
      if (existing) {
        existing.approval_id = p.approval_id || existing.approval_id
        existing.interactId = p.approval_id || existing.interactId
        if (qData && Object.keys(qData).length) existing.data = { ...(existing.data || {}), ...qData }
        if (qContent) existing.content = qContent
      } else {
        st.msgs.push({
          role: 'assistant',
          type: isQ ? 'question' : 'approval',
          approval_id: p.approval_id,
          interactId: p.approval_id,
          toolName: p.tool_name,
          arguments: p.arguments,
          data: qData,
          content: qContent,
          status: 'pending',
          _key: `${event.run_id}_approval_${p.approval_id}`,
        })
      }
    } else if (event.type === 'done') {
      const streamed = st.msgs.slice(st.segStart).some(m => m.role === 'assistant' && m.type === 'text' && m.content)
      const rows = (p.messages || []).filter((m: any) => {
        if (m.type === 'approval' || m.type === 'question') return !st.msgs.some(x => x.approval_id === m.approval_id)
        return !streamed
      })
      appendServerMessages(st, rows, event.session_id)
      handleDone(st)
      if (p.error && p.error !== 'cancelled') handleError(st, p.error)
      else if (p.error === 'cancelled') st.status = '已停止'
      else if (p.pending) { st.awaiting = true; st.status = '等待交互…' }
    }
  }

  /** 引擎事件按会话路由：后台会话的事件写进它自己的状态，不会串到当前会话。 */
  const handleRuntimeEvent = (event: RuntimeEvent) => {
    const st = runs.get(event.session_id)
    if (!st) return
    applyRuntimeEvent(st, event)
  }

  /** 旧通道（chat:* / approval:pending 等）的事件带不带 session_id 都有：没带就当作当前会话。 */
  function routeLegacy(payload: any): SessionRun | undefined {
    const sid = String(payload?.session_id || payload?.sessionId || '')
    return sid ? runs.get(sid) : cur.value
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

    // 所有会话的运行事件都收，由 handleRuntimeEvent 按 session_id 分发。
    unsubs.push(runtime.subscribe(null, handleRuntimeEvent))

    // ── Core chat events (actual bridge event names) ──────────────────────
    sub('chat:chunk', (payload: any) => {
      const st = routeLegacy(payload)
      if (!st || st.runID || st.starting) return
      const text = String(payload?.delta || payload?.content || payload?.text || '')
      handleChunk(st, text)
    })

    sub('chat:done', (payload: any) => {
      const st = routeLegacy(payload)
      if (!st || st.runID || st.starting) return
      if (payload?.error) handleError(st, payload.error)
      else {
        if (payload?.resume && Array.isArray(payload?.messages)) {
          appendServerMessages(st, payload.messages, st.sid)
        }
        handleDone(st)
      }
    })

    sub('a2ui:render', handleA2UI)

    sub('approval:pending', (payload: any) => {
      const st = routeLegacy(payload)
      if (!st || st.runID || st.starting) return
      st.awaiting = true
      st.sending = false
      st.status = '等待批准…'
      const idx = st.streamIdx
      if (idx >= 0 && st.msgs[idx]?.streaming) {
        st.msgs[idx] = {
          ...st.msgs[idx], streaming: false, type: 'approval',
          approval_id: payload?.approval_id || payload?.id || '',
          content: payload?.prompt || '需要您的批准才能继续',
        }
        st.streamIdx = -1
      }
    })

    sub('ask:pending', (payload: any) => {
      const st = routeLegacy(payload)
      if (!st) return
      const q = payload?.question
      const qData = parseQuestionData(typeof q === 'object' && q ? q : {}, payload?.arguments)
      const questionText = typeof q === 'string' ? q
        : (qData?.text || qData?.question || qData?.prompt || '请回答：')
      st.awaiting = true
      st.sending = false
      st.status = '等待输入…'
      const incomingId = payload?.approval_id || payload?.interrupt_id || payload?.interact_id || ''
      const existing = st.msgs.find(m =>
        m.type === 'question' && !m.resolved && (!incomingId || m.approval_id === incomingId || m.interactId === incomingId || !m.data?.choices?.length)
      )
      if (existing) {
        existing.content = questionText || existing.content
        existing.data = { ...(existing.data || {}), ...qData }
        existing.interactId = payload?.interrupt_id || payload?.interact_id || existing.interactId || incomingId
        existing.approval_id = payload?.approval_id || existing.approval_id || incomingId
        return
      }
      const idx = st.streamIdx
      const nextQuestion: Message = {
        role: 'assistant', type: 'question',
        content: questionText,
        data: qData,
        interactId: payload?.interrupt_id || payload?.interact_id || incomingId,
        approval_id: payload?.approval_id || payload?.interrupt_id || incomingId,
        _key: `${st.sid}_${chatPaneKey.value}_question_${Date.now()}`,
      }
      if (st.runID) {
        if (idx >= 0 && st.msgs[idx]) st.msgs[idx].streaming = false
        st.streamIdx = -1
        st.msgs.push(nextQuestion)
      } else if (idx >= 0 && st.msgs[idx]?.streaming) {
        st.msgs[idx] = {
          ...st.msgs[idx],
          ...nextQuestion,
          streaming: false,
        }
        st.streamIdx = -1
      } else {
        st.msgs.push(nextQuestion)
      }
    })

    sub('chat:paused', (payload: any) => {
      const st = routeLegacy(payload)
      if (!st || st.runID || st.starting) return
      st.awaiting = true
      st.sending = false
      if (!st.status || st.status === '生成中…' || st.status === '思考中…') {
        st.status = '等待交互…'
      }
    })

    // ── Agent trace events — used for tool call status display ─────────────
    sub('agent:trace', (payload: any) => {
      const st = routeLegacy(payload)
      if (!st || st.runID || st.starting) return
      if (!st.sending) return
      const component = String(payload?.component || '')
      const phase = String(payload?.phase || '')
      const name = String(payload?.name || payload?.detail || '')
      if (component === 'Tool') {
        if (phase === 'start') {
          st.status = `🔧 调用 ${name}…`
        } else if (phase === 'end') {
          st.status = '生成中…'
        } else if (phase === 'error') {
          st.status = `⚠️ ${name} 出错`
        }
      } else if (component === 'Runner' || component === 'Memory') {
        if (phase === 'start' && name) {
          st.status = name === 'recall' ? '检索记忆…' : '思考中…'
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
    awaitingInteract, runStatusLine, chatPaneKey, streamMsgIndex, formInputs, runBadges,
    loadSessions, newSession, selectSession, deleteSession, renameSession, forkSession, forking, deleteSessionsBatch, deleteOtherSessions, deleteSessionsByTitle, clearSession, stopGeneration, stopSessionById, sendMessage,
    handleApprove, handleReject, steerSession, submitQuestion, submitAUIForm, submitAUIAction,
    reloadCurrentSession, setupWailsEvents, cleanup,
  }
}
