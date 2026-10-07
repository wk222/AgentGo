import { defineComponent, ref, computed, nextTick, watch, onMounted, onBeforeUnmount, PropType } from 'vue'
import type { Message, Session } from './composables/useChat'
import { sessionTitle, sessionIdOf } from './composables/useChat'
import { wailsCall } from './wails'
import ReasoningBubble from './components/chat/ReasoningBubble'
import ToolCallBubble from './components/chat/ToolCallBubble'
import PromptSuggestions from './components/chat/PromptSuggestions'

function extractThinking(content: string): { thinking: string; main: string } {
  if (!content) return { thinking: '', main: '' }
  const match = content.match(/<think>([\s\S]*?)<\/think>/i)
  if (match) {
    return {
      thinking: match[1].trim(),
      main: content.replace(/<think>[\s\S]*?<\/think>/i, '').trim(),
    }
  }
  const openMatch = content.match(/<think>([\s\S]*)$/i)
  if (openMatch) {
    return {
      thinking: openMatch[1].trim(),
      main: content.replace(/<think>[\s\S]*$/i, '').trim(),
    }
  }
  return { thinking: '', main: content }
}

/* ── A2UI Card components ── */
const AUICard = ({
  msg, index, formInputs, onSubmitAUI, onSubmitAUIAction, onFormInput,
}: {
  msg: Message; index: number; formInputs: Record<string, any>
  onSubmitAUI: (m: Message, v: string, i: number) => void
  onSubmitAUIAction: (m: Message, v: string, i: number) => void
  onFormInput: (key: string, field: string, val: any) => void
}) => {
  const comp = String(msg.component || 'card').toLowerCase()
  const data = msg.data || {}
  const title = msg.content || data.title || data.heading || data.name || ''
  const actions: any[] = Array.isArray(data.actions) ? data.actions : []
  const surface = msg.surface || data.__surface || 'chat'

  const Actions = () => actions.length > 0 ? (
    <div class="aui-actions">
      {actions.map((a: any, i: number) => (
        <button
          key={i}
          class={['aui-action-btn', i === 0 && 'primary']}
          disabled={!!msg.resolved}
          onClick={() => onSubmitAUIAction(msg, a.value || a.id || a.label, index)}
        >{a.label || a.title || `操作 ${i + 1}`}</button>
      ))}
    </div>
  ) : null

  if (surface !== 'chat') {
    data.surface = surface
  }

  /* Markdown */
  if (comp.includes('markdown')) {
    const text = data.markdown || data.content || data.text || title
    return (
      <div class="aui-card aui-markdown">
        {title && text !== title && <div class="aui-title">{title}</div>}
        <div class="aui-markdown-text">{String(text || '')}</div>
        <Actions />
      </div>
    )
  }

  /* Code block */
  if (comp.includes('code')) {
    const code = data.code || data.content || data.text || ''
    const language = data.language || data.lang || 'text'
    return (
      <div class="aui-card aui-code-card">
        {title && <div class="aui-title">{title}</div>}
        <div class="aui-code-lang">{language}</div>
        <pre class="aui-code-block"><code>{String(code)}</code></pre>
        <Actions />
      </div>
    )
  }

  /* Progress */
  if (comp.includes('progress')) {
    const value = Math.max(0, Math.min(100, Number(data.value ?? data.percent ?? data.progress ?? 0)))
    return (
      <div class="aui-card aui-progress-card">
        {title && <div class="aui-title">{title}</div>}
        <div class="aui-progress-track"><div class="aui-progress-fill" style={{ width: `${value}%` }}></div></div>
        <div class="aui-progress-meta">
          <span>{data.label || data.message || '进度'}</span>
          <strong>{value}%</strong>
        </div>
        <Actions />
      </div>
    )
  }

  /* List */
  if (comp.includes('list')) {
    const items: any[] = Array.isArray(data.items) ? data.items
      : Array.isArray(data.rows) ? data.rows
      : Array.isArray(data) ? data : []
    return (
      <div class="aui-card aui-list-card">
        {title && <div class="aui-title">{title}</div>}
        <div class="aui-list">
          {items.slice(0, 20).map((item: any, i: number) => (
            <div key={i} class="aui-list-item">
              <span class="aui-list-index">{i + 1}</span>
              <span>{typeof item === 'object' ? (item.title || item.label || item.name || JSON.stringify(item)) : String(item)}</span>
            </div>
          ))}
        </div>
        <Actions />
      </div>
    )
  }

  /* Accordion */
  if (comp.includes('accordion')) {
    const items: any[] = Array.isArray(data.items) ? data.items : []
    return (
      <div class="aui-card aui-accordion-card">
        {title && <div class="aui-title">{title}</div>}
        {items.map((item: any, i: number) => (
          <details key={i} class="aui-accordion-item" open={i === 0}>
            <summary>{item.title || item.label || `条目 ${i + 1}`}</summary>
            <div>{item.content || item.description || item.text || ''}</div>
          </details>
        ))}
        <Actions />
      </div>
    )
  }

  /* Image gallery */
  if (comp.includes('image')) {
    const images: any[] = Array.isArray(data.images) ? data.images
      : Array.isArray(data.items) ? data.items
      : []
    return (
      <div class="aui-card aui-image-gallery">
        {title && <div class="aui-title">{title}</div>}
        <div class="aui-image-grid">
          {images.slice(0, 8).map((img: any, i: number) => {
            const src = typeof img === 'string' ? img : (img.url || img.src)
            const alt = typeof img === 'string' ? '' : (img.alt || img.title || '')
            return src ? <img key={i} src={src} alt={alt} /> : null
          })}
        </div>
        <Actions />
      </div>
    )
  }

  /* Metrics */
  if (comp.includes('metric')) {
    const metrics: any[] = Array.isArray(data.metrics) ? data.metrics : []
    return (
      <div class="aui-card aui-metrics">
        {title && <div class="aui-title">{title}</div>}
        <div class="aui-metrics-grid">
          {metrics.map((m: any, i: number) => (
            <div key={i} class="aui-metric-item">
              <div class="aui-metric-value">{m.value ?? m.count ?? '—'}</div>
              <div class="aui-metric-label">{m.label || m.name || `指标 ${i + 1}`}</div>
              {m.change != null && (
                <div class={['aui-metric-change', m.change > 0 ? 'up' : 'down']}>
                  {m.change > 0 ? '↑' : '↓'} {Math.abs(Number(m.change))}
                </div>
              )}
            </div>
          ))}
        </div>
        <Actions />
      </div>
    )
  }

  /* Table */
  if (comp.includes('table')) {
    const rows: any[] = Array.isArray(data) ? data
      : Array.isArray(data.rows) ? data.rows
      : Array.isArray(data.records) ? data.records
      : []
    const cols = rows.length > 0 ? Object.keys(rows[0]).slice(0, 6) : []
    return (
      <div class="aui-card aui-table-card">
        {title && <div class="aui-title">{title}</div>}
        <div class="aui-table-wrap">
          <table class="aui-table">
            <thead><tr>{cols.map(c => <th key={c}>{c}</th>)}</tr></thead>
            <tbody>
              {rows.slice(0, 12).map((row: any, i: number) => (
                <tr key={i}>{cols.map(c => <td key={c}>{String(row[c] ?? '')}</td>)}</tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    )
  }

  /* Chart / Bar */
  if (comp.includes('chart') || comp.includes('bar')) {
    const rows: any[] = Array.isArray(data.rows) ? data.rows
      : Array.isArray(data) ? data : []
    const max = Math.max(1, ...rows.map((r: any) => Number(r.value ?? r.count ?? 0)))
    return (
      <div class="aui-card aui-chart">
        {title && <div class="aui-title">{title}</div>}
        <div class="aui-bars">
          {rows.slice(0, 8).map((row: any, i: number) => {
            const val = Number(row.value ?? row.count ?? 0)
            const label = row.label || row.name || row.category || String(i + 1)
            return (
              <div key={i} class="aui-bar-row">
                <span class="aui-bar-label">{label}</span>
                <div class="aui-bar-track">
                  <div class="aui-bar-fill" style={{ width: Math.max(4, Math.round(val / max * 100)) + '%' }}></div>
                </div>
                <span class="aui-bar-val">{val}</span>
              </div>
            )
          })}
        </div>
      </div>
    )
  }

  /* Timeline */
  if (comp.includes('timeline')) {
    const items: any[] = Array.isArray(data.items) ? data.items
      : Array.isArray(data.steps) ? data.steps : []
    return (
      <div class="aui-card aui-timeline">
        {title && <div class="aui-title">{title}</div>}
        <div class="aui-timeline-list">
          {items.map((item: any, i: number) => (
            <div key={i} class={['aui-tl-item', item.status || '']}>
              <div class="aui-tl-dot"></div>
              <div class="aui-tl-content">
                <div class="aui-tl-title">{item.title || item.name || item.step || ''}</div>
                {item.description && <div class="aui-tl-desc">{item.description}</div>}
                {item.time && <div class="aui-tl-time">{item.time}</div>}
              </div>
            </div>
          ))}
        </div>
      </div>
    )
  }

  /* Form */
  if (comp.includes('form')) {
    const fields: any[] = Array.isArray(data.fields) ? data.fields : []
    const key = msg.interactId || String(index)
    const formData = formInputs[key] || {}
    return (
      <div class={['aui-card aui-form', msg.resolved && 'resolved']}>
        {title && <div class="aui-title">{title}</div>}
        {data.description && <div class="aui-form-desc">{data.description}</div>}
        <div class="aui-form-fields">
          {fields.map((f: any, i: number) => {
            const fname = f.name || f.key || `field_${i}`
            return (
              <div key={fname} class="aui-form-field">
                <label class="aui-field-label">{f.label || fname}</label>
                {f.type === 'select' ? (
                  <select
                    class="aui-field-input"
                    disabled={msg.resolved}
                    value={formData[fname] ?? f.default ?? ''}
                    onChange={(e: Event) => onFormInput(key, fname, (e.target as HTMLSelectElement).value)}
                  >
                    {(f.options || []).map((opt: any) => (
                      <option key={opt.value ?? opt} value={opt.value ?? opt}>{opt.label ?? opt}</option>
                    ))}
                  </select>
                ) : f.type === 'textarea' ? (
                  <textarea
                    class="aui-field-input"
                    rows={3}
                    placeholder={f.placeholder || ''}
                    disabled={msg.resolved}
                    value={formData[fname] ?? f.default ?? ''}
                    onInput={(e: Event) => onFormInput(key, fname, (e.target as HTMLTextAreaElement).value)}
                  ></textarea>
                ) : (
                  <input
                    class="aui-field-input"
                    type={f.type === 'number' ? 'number' : f.type === 'password' ? 'password' : 'text'}
                    placeholder={f.placeholder || ''}
                    disabled={msg.resolved}
                    value={formData[fname] ?? f.default ?? ''}
                    onInput={(e: Event) => onFormInput(key, fname, (e.target as HTMLInputElement).value)}
                  />
                )}
              </div>
            )
          })}
        </div>
        {!msg.resolved && actions.length > 0 && (
          <div class="aui-actions">
            {actions.map((a: any, i: number) => (
              <button
                key={i}
                class={['aui-action-btn', i === 0 && 'primary']}
                onClick={() => onSubmitAUI(msg, a.value || a.id || a.label, index)}
              >{a.label || a.title || `操作 ${i + 1}`}</button>
            ))}
          </div>
        )}
        {msg.resolved && <div class="aui-resolved-badge">已提交 ✓</div>}
      </div>
    )
  }

  /* Status */
  if (comp.includes('status')) {
    const status = data.status || 'info'
    const statusClass = { success: 'ok', error: 'fail', warning: 'warn', info: 'info' }[status] || 'info'
    const icon = status === 'success' ? '✓' : status === 'error' ? '✗' : 'ℹ'
    return (
      <div class={['aui-card aui-status', statusClass]}>
        <div class="aui-status-icon">{icon}</div>
        <div class="aui-status-text">
          {title && <div class="aui-title">{title}</div>}
          {data.message && <div class="aui-status-msg">{data.message}</div>}
        </div>
      </div>
    )
  }

  /* Default key-value card */
  const entries = Object.entries(data)
    .filter(([k]) => !['title', 'heading', 'name', 'actions', 'component', 'rows', 'items', 'fields', 'metrics', 'records'].includes(k))
    .filter(([, v]) => !Array.isArray(v) && typeof v !== 'object')
    .slice(0, 10)
  return (
    <div class="aui-card">
      {title && <div class="aui-title">{title}</div>}
      {entries.length > 0 && (
        <div class="aui-kv-list">
          {entries.map(([k, v]) => (
            <div key={k} class="aui-kv-row">
              <span class="aui-kv-key">{k}</span>
              <span class="aui-kv-val">{String(v)}</span>
            </div>
          ))}
        </div>
      )}
      <Actions />
    </div>
  )
}

/* ── Main ChatPanel ── */
export default defineComponent({
  name: 'ChatPanel',
  props: {
    messages: { type: Array as PropType<Message[]>, required: true },
    sessionId: { type: String, default: '' },
    sessions: { type: Array as PropType<Session[]>, default: () => [] },
    sessionLoading: { type: Boolean, default: false },
    sending: { type: Boolean, default: false },
    awaitingInteract: { type: Boolean, default: false },
    runStatusLine: { type: String, default: '' },
    chatPaneKey: { type: Number, default: 0 },
    workspacePanelOpen: { type: Boolean, default: true },
    formInputs: { type: Object as PropType<Record<string, Record<string, any>>>, default: () => ({}) },
    onSend: { type: Function as PropType<(text: string, images?: string[]) => void>, required: true },
    onStop: { type: Function as PropType<() => void>, required: true },
    onToggleWorkspace: { type: Function as PropType<() => void>, required: true },
    onReload: { type: Function as PropType<() => void>, required: true },
    onNewChat: { type: Function as PropType<() => void>, default: () => {} },
    onClearChat: { type: Function as PropType<() => void>, default: () => {} },
    onSelectSession: { type: Function as PropType<(id: string) => void>, default: () => {} },
    onDeleteSession: { type: Function as PropType<(id: string) => void>, default: () => {} },
    onApprove: { type: Function as PropType<(id: string) => void>, required: true },
    onReject: { type: Function as PropType<(id: string) => void>, required: true },
    onSubmitAUI: { type: Function as PropType<(msg: Message, val: string, idx: number) => void>, required: true },
    onSubmitAUIAction: { type: Function as PropType<(msg: Message, val: string, idx: number) => void>, required: true },
    onSubmitQuestion: { type: Function as PropType<(msg: Message, answer: string, idx: number) => void>, required: true },
    onFormInput: { type: Function as PropType<(key: string, field: string, val: any) => void>, required: true },
    modeProfile: { type: String, default: 'assistant' },
    modeCanvas: { type: String, default: 'balanced' },
    modeSaving: { type: Boolean, default: false },
    onModeChange: { type: Function as PropType<(profile: string, canvas: string) => void>, required: true },
  },
  setup(props) {
    const inputText = ref('')
    const threadEl = ref<HTMLElement>()
    const textareaEl = ref<HTMLTextAreaElement>()
    const questionInput = ref<Record<string, string>>({})
    const questionChoices = ref<Record<string, string[]>>({})
    const pastedImages = ref<string[]>([])

    const scrollToBottom = async () => {
      await nextTick()
      if (threadEl.value) threadEl.value.scrollTop = threadEl.value.scrollHeight
    }

    watch(() => [props.messages.length, props.chatPaneKey], () => { void scrollToBottom() })

    const handleSend = async () => {
      const text = inputText.value.trim()
      if (!text && pastedImages.value.length === 0) return
      if (text === '/new' || text === '/clear') {
        inputText.value = ''
        if (textareaEl.value) textareaEl.value.style.height = 'auto'
        if (text === '/new') props.onNewChat(); else props.onClearChat()
        return
      }
      if (props.sending) {
        // Codex & Cursor parity: Mid-turn Steer!
        inputText.value = ''
        try {
          const res = await wailsCall<{ success?: boolean; steered?: boolean }>('SteerSession', props.sessionId, text)
          if (res?.steered) {
            console.info('[ChatPanel] Mid-turn steer queued:', text)
          } else {
            console.warn('[ChatPanel] SteerSession rejected, session may not be actively running')
          }
        } catch (e) {
          console.error('[ChatPanel] SteerSession failed:', e)
        }
        return
      }
      inputText.value = ''
      const imgs = [...pastedImages.value]
      pastedImages.value = []
      props.onSend(text, imgs)
    }

    const handlePaste = async (e: ClipboardEvent) => {
      const items = e.clipboardData?.items
      if (!items) return
      for (const item of items) {
        if (item.type.indexOf('image') !== -1) {
          const file = item.getAsFile()
          if (!file) continue
          const reader = new FileReader()
          reader.onload = async (event) => {
            const dataUrl = event.target?.result as string
            if (!dataUrl) return
            const commaIdx = dataUrl.indexOf(',')
            if (commaIdx === -1) return
            const base64Data = dataUrl.substring(commaIdx + 1)
            const mimeType = file.type
            try {
              const res = await wailsCall('UploadAttachment', base64Data, mimeType)
              if (res) {
                pastedImages.value.push(res)
              }
            } catch (err: any) {
              console.error('Failed to upload attachment:', err)
              alert('上传图片失败: ' + err.message)
            }
          }
          reader.readAsDataURL(file)
        }
      }
    }

    interface MentionOption {
      id: string
      label: string
      desc: string
      icon: string
      insertText: string
    }

    const showMentionMenu = ref(false)
    const mentionFilter = ref('')
    const mentionIndex = ref(0)
    const mentionStartIndex = ref(-1)
    const workspaceFiles = ref<string[]>([])

    const BUILTIN_MENTIONS: MentionOption[] = [
      { id: 'codebase', label: '@Codebase', desc: '检索整个工作区项目代码与语义上下文', icon: '⚡', insertText: '@Codebase ' },
      { id: 'git', label: '@Git', desc: '引入当前分支改动、未提交 Diff 与状态', icon: '🌿', insertText: '@Git ' },
      { id: 'terminal', label: '@Terminal', desc: '引入当前终端最近会话与运行输出', icon: '📟', insertText: '@Terminal ' },
      { id: 'problems', label: '@Problems', desc: '引入当前工作区代码错误与诊断信息', icon: '⚠️', insertText: '@Problems ' },
    ]

    const fetchWorkspaceFiles = async () => {
      if (workspaceFiles.value.length > 0) return
      try {
        const res = await wailsCall<any>('SearchFiles', '', 50)
        if (Array.isArray(res) && res.length > 0) {
          workspaceFiles.value = res
        } else {
          const list = await wailsCall<any[]>('ListWorkspace', '')
          if (Array.isArray(list)) {
            workspaceFiles.value = list.map(item => item.path || item.name).filter(Boolean)
          }
        }
      } catch (e) {
        console.debug('[ChatPanel] fetchWorkspaceFiles failed:', e)
      }
    }

    const filteredMentions = computed(() => {
      const q = mentionFilter.value.toLowerCase()
      const builtin = BUILTIN_MENTIONS.filter(m =>
        m.label.toLowerCase().includes(q) || m.desc.toLowerCase().includes(q)
      )
      const files = workspaceFiles.value
        .filter(f => f.toLowerCase().includes(q))
        .slice(0, 10)
        .map(f => ({
          id: `file:${f}`,
          label: `@${f.split(/[\/\\]/).pop()}`,
          desc: f,
          icon: '📄',
          insertText: `@${f} `,
        }))
      return [...builtin, ...files]
    })

    const selectMention = (opt: MentionOption) => {
      const start = mentionStartIndex.value
      const curPos = textareaEl.value?.selectionStart || (start + 1 + mentionFilter.value.length)
      const before = inputText.value.slice(0, start)
      const after = inputText.value.slice(curPos)
      inputText.value = before + opt.insertText + after
      showMentionMenu.value = false
      nextTick(() => {
        if (textareaEl.value) {
          const newPos = (before + opt.insertText).length
          textareaEl.value.focus()
          textareaEl.value.setSelectionRange(newPos, newPos)
        }
      })
    }

    const handleInput = (e: Event) => {
      const target = e.target as HTMLTextAreaElement
      inputText.value = target.value
      autoResize(e)

      const pos = target.selectionStart || 0
      const beforeCursor = target.value.slice(0, pos)
      const lastAt = beforeCursor.lastIndexOf('@')

      if (lastAt !== -1 && (lastAt === 0 || /\s/.test(beforeCursor[lastAt - 1]))) {
        const query = beforeCursor.slice(lastAt + 1)
        if (!/\s/.test(query)) {
          mentionFilter.value = query
          mentionStartIndex.value = lastAt
          showMentionMenu.value = true
          mentionIndex.value = 0
          fetchWorkspaceFiles()
          return
        }
      }
      showMentionMenu.value = false
    }

    const handleKeydown = (e: KeyboardEvent) => {
      if (showMentionMenu.value && filteredMentions.value.length > 0) {
        if (e.key === 'ArrowDown') {
          e.preventDefault()
          mentionIndex.value = (mentionIndex.value + 1) % filteredMentions.value.length
          return
        }
        if (e.key === 'ArrowUp') {
          e.preventDefault()
          mentionIndex.value = (mentionIndex.value - 1 + filteredMentions.value.length) % filteredMentions.value.length
          return
        }
        if (e.key === 'Enter' || e.key === 'Tab') {
          e.preventDefault()
          selectMention(filteredMentions.value[mentionIndex.value])
          return
        }
        if (e.key === 'Escape') {
          e.preventDefault()
          showMentionMenu.value = false
          return
        }
      }

      if (e.key === 'Enter' && !e.shiftKey && !e.metaKey) {
        e.preventDefault()
        handleSend()
      }
    }

    const autoResize = (e: Event) => {
      const el = e.target as HTMLTextAreaElement
      el.style.height = 'auto'
      el.style.height = Math.min(el.scrollHeight, 160) + 'px'
    }

    const currentSession = () => props.sessions.find(s =>
      (s.id || s.session_id || '') === props.sessionId
    )

    const historyOpen = ref(false)
    const fmtTime = (v: any): string => {
      if (v === undefined || v === null || v === '') return ''
      let ms = typeof v === 'number' ? v : Number(v)
      if (!isFinite(ms) || isNaN(ms)) ms = Date.parse(String(v))
      if (!isFinite(ms) || isNaN(ms)) return ''
      if (ms < 1e12) ms *= 1000
      const diff = Date.now() - ms
      const m = Math.floor(diff / 60000)
      if (m < 1) return '刚刚'
      if (m < 60) return m + ' 分钟前'
      const h = Math.floor(m / 60)
      if (h < 24) return h + ' 小时前'
      const d = Math.floor(h / 24)
      if (d < 30) return d + ' 天前'
      return new Date(ms).toLocaleDateString()
    }
    const doNew = () => { historyOpen.value = false; props.onNewChat() }
    const doClear = () => {
      historyOpen.value = false
      if (!props.sessionId) return
      if (window.confirm('清除当前会话？该会话的全部消息将被删除。')) props.onClearChat()
    }
    const globalKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey)) return
      const k = e.key.toLowerCase()
      if (k === 'l' && !e.shiftKey) { e.preventDefault(); doNew() }
      else if (k === 'l' && e.shiftKey) { e.preventDefault(); doClear() }
    }
    onMounted(() => window.addEventListener('keydown', globalKey))
    onBeforeUnmount(() => window.removeEventListener('keydown', globalKey))

    const setModeProfile = (profile: string) => props.onModeChange(profile, props.modeCanvas)
    const setModeCanvas = (canvas: string) => props.onModeChange(props.modeProfile, canvas)

    const toggleQuestionChoice = (key: string, value: string) => {
      const list = questionChoices.value[key] || []
      questionChoices.value[key] = list.includes(value)
        ? list.filter(x => x !== value)
        : [...list, value]
    }

    const SUGGESTIONS = [
      '帮我分析一下数据集的关键指标',
      '制定一个工作计划并拆解任务',
      '写一份技术方案文档',
    ]

    return () => {
      const hasMessages = props.messages.some(m =>
        m.type === 'aui' || m.role === 'user' || (m.role === 'assistant' && (m.content || '').trim())
      )
      const isWelcome = !hasMessages && !props.sessionLoading

      const sess = currentSession()
      const title = sess ? sessionTitle(sess) : (props.sessionId ? '对话' : '新对话')

      return (
        <div style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
          {/* Top bar */}
          <div class="chat-topbar">
            <span class="chat-topbar-title">{title}</span>
            <button class="topbar-btn topbar-btn-accent" onClick={doNew} title="新会话 (Ctrl+L，或输入 /new)">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
            </button>
            <div class="chat-history-wrap">
              <button class={['topbar-btn', historyOpen.value && 'active']} onClick={() => { historyOpen.value = !historyOpen.value }} title="历史会话">
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><polyline points="12 7 12 12 15 14"/></svg>
              </button>
              {historyOpen.value && (
                <>
                  <div class="chat-history-backdrop" onClick={() => { historyOpen.value = false }}></div>
                  <div class="chat-history-pop">
                    <div class="chat-history-head">
                      <span>最近会话</span>
                      <button class="chat-history-new" onClick={doNew}>+ 新会话</button>
                    </div>
                    <div class="chat-history-list">
                      {props.sessions.length === 0 && <div class="chat-history-empty">暂无历史会话</div>}
                      {props.sessions.map((s) => {
                        const sid = sessionIdOf(s)
                        const active = sid === props.sessionId
                        return (
                          <div key={sid} class={['chat-history-item', active && 'active']}
                            onClick={() => { historyOpen.value = false; if (!active) props.onSelectSession(sid) }}>
                            <div class="chat-history-main">
                              <div class="chat-history-title">{sessionTitle(s)}</div>
                              <div class="chat-history-meta">{fmtTime((s as any).updated_at)}{(s as any).message_count ? ' · ' + (s as any).message_count + ' 条' : ''}</div>
                            </div>
                            <button class="chat-history-del" title="删除"
                              onClick={(e: MouseEvent) => { e.stopPropagation(); props.onDeleteSession(sid) }}>
                              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14H6L5 6"/><path d="M10 11v6M14 11v6"/></svg>
                            </button>
                          </div>
                        )
                      })}
                    </div>
                  </div>
                </>
              )}
            </div>
            <button class="topbar-btn" onClick={doClear} title="清除当前会话 (Ctrl+Shift+L，或输入 /clear)">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M19 6l-1 14H6L5 6"/></svg>
            </button>
            <div class="chat-mode-controls">
              <select
                class="chat-mode-select"
                value={props.modeProfile}
                disabled={props.modeSaving}
                title="智能体模式"
                onChange={(e: Event) => setModeProfile((e.target as HTMLSelectElement).value)}
              >
                <option value="assistant">Assistant</option>
                <option value="app_matrix">App Matrix</option>
                <option value="admin">Admin</option>
              </select>
              <select
                class="chat-mode-select"
                value={props.modeCanvas}
                disabled={props.modeSaving}
                title="执行深度"
                onChange={(e: Event) => setModeCanvas((e.target as HTMLSelectElement).value)}
              >
                <option value="focused">Focused</option>
                <option value="balanced">Balanced</option>
                <option value="deep">Deep</option>
              </select>
            </div>

            <button class="topbar-btn" onClick={props.onReload} title="重新加载">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 .49-3.15"/></svg>
            </button>

            <button
              class="topbar-btn"
              onClick={props.onToggleWorkspace}
              title={props.workspacePanelOpen ? '隐藏工作区' : '显示工作区'}
            >
              {props.workspacePanelOpen
                ? <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M15 3v18"/></svg>
                : <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/></svg>
              }
            </button>
          </div>

          {/* Thread */}
          <div class="chat-thread" ref={threadEl} key={props.chatPaneKey}>
            {props.sessionLoading && (
              <div class="chat-loading">
                <div class="chat-loading-dots">
                  <span></span><span></span><span></span>
                </div>
                <span>加载会话…</span>
              </div>
            )}

            {!props.sessionLoading && isWelcome && (
              <div class="chat-empty">
                <div class="chat-empty-icon">✦</div>
                <h3>AgentGo</h3>
                <p>本地桌面智能体客户端，由 Wails v3 + Eino 驱动</p>
                <div class="chat-suggestions">
                  {SUGGESTIONS.map((s, i) => (
                    <button key={i} class="chat-suggestion" onClick={() => { inputText.value = s; handleSend() }}>
                      {s}
                    </button>
                  ))}
                </div>
              </div>
            )}

            {!props.sessionLoading && !isWelcome && props.messages.map((msg, index) => {
              if (msg.type === 'aui') {
                return (
                  <div key={msg._key || index} class="msg-row assistant">
                    <AUICard
                      msg={msg}
                      index={index}
                      formInputs={props.formInputs}
                      onSubmitAUI={props.onSubmitAUI}
                      onSubmitAUIAction={props.onSubmitAUIAction}
                      onFormInput={props.onFormInput}
                    />
                  </div>
                )
              }

              if (msg.type === 'approval') {
                return (
                  <div key={msg._key || index} class="msg-row assistant">
                    <div class={['msg-approval', msg.resolved && 'resolved']}>
                      <div class="msg-approval-label">{msg.resolved ? '已处理' : '需要批准'}</div>
                      <div class="msg-approval-prompt">{msg.content || '需要您的批准才能继续'}</div>
                      {msg.resolved && <div class="msg-approval-state">Agent 已收到您的选择并继续执行。</div>}
                      <div class="msg-approval-actions">
                        <button class="msg-approve-btn ok" disabled={msg.resolved} onClick={() => props.onApprove(msg.approval_id || '')}>批准</button>
                        <button class="msg-approve-btn no" disabled={msg.resolved} onClick={() => props.onReject(msg.approval_id || '')}>拒绝</button>
                      </div>
                    </div>
                  </div>
                )
              }

              if (msg.type === 'question') {
                const key = msg.interactId || String(index)
                const qData = msg.data || {}
                const choices: any[] = Array.isArray(qData.choices) ? qData.choices : []
                const multiple = !!qData.multiple
                const freeText = qData.free_text !== false && qData.freeText !== false
                const selected = questionChoices.value[key] || []
                return (
                  <div key={msg._key || index} class="msg-row assistant">
                    <div class={['msg-approval', msg.resolved && 'resolved']} style={{ borderLeftColor: 'var(--info)' }}>
                      <div class="msg-approval-label" style={{ color: 'var(--info)' }}>需要输入</div>
                      <div class="msg-approval-prompt">{msg.content || '请输入您的回答：'}</div>
                      {choices.length > 0 && (
                        <div class="msg-choice-list">
                          {choices.map((c: any, i: number) => {
                            const val = String(c.id || c.value || c.label || c.title || i)
                            const label = c.label || c.title || c.name || val
                            return multiple ? (
                              <label key={val} class="msg-choice-check">
                                <input
                                  type="checkbox"
                                  checked={selected.includes(val)}
                                  disabled={msg.resolved}
                                  onChange={() => toggleQuestionChoice(key, val)}
                                />
                                <span>{label}</span>
                              </label>
                            ) : (
                              <button
                                key={val}
                                class="msg-choice-btn"
                                disabled={msg.resolved}
                                onClick={() => props.onSubmitQuestion(msg, val, index)}
                              >{label}</button>
                            )
                          })}
                        </div>
                      )}
                      <div style={{ display: 'flex', gap: '8px' }}>
                        {freeText && (
                          <input
                            style={{ flex: 1, padding: '6px 10px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)', fontSize: '13px' }}
                            placeholder="输入回答…"
                            disabled={msg.resolved}
                            value={questionInput.value[key] || ''}
                            onInput={(e: Event) => questionInput.value[key] = (e.target as HTMLInputElement).value}
                            onKeydown={(e: KeyboardEvent) => {
                              if (e.key === 'Enter') props.onSubmitQuestion(msg, questionInput.value[key] || '', index)
                            }}
                          />
                        )}
                        <button
                          class="msg-approve-btn ok"
                          disabled={msg.resolved}
                          onClick={() => {
                            const answer = multiple ? JSON.stringify(selected) : (questionInput.value[key] || selected[0] || '')
                            props.onSubmitQuestion(msg, answer, index)
                          }}
                        >{msg.resolved ? '已提交' : '发送'}</button>
                      </div>
                    </div>
                  </div>
                )
              }

              if (msg.type === 'tool_call') {
                return (
                  <div key={msg._key || index} class="msg-row assistant tool-call-row">
                    <ToolCallBubble
                      toolName={msg.toolName || 'tool'}
                      arguments={msg.arguments}
                      content={msg.content}
                      status={msg.status}
                    />
                  </div>
                )
              }

              /* Text message */
              const { thinking, main } = extractThinking(msg.content || '')
              return (
                <div key={msg._key || index} class={['msg-row', msg.role]}>
                  {msg.role === 'user' ? (
                    <div class="msg-bubble user-bubble">
                      {msg.content}
                      {msg.meta?.images && msg.meta.images.length > 0 && (
                        <div class="msg-images" style={{ display: 'flex', gap: '8px', marginTop: msg.content ? '8px' : '0', flexWrap: 'wrap' }}>
                          {msg.meta.images.map((imgUrl: string) => (
                            <img
                              key={imgUrl}
                              src={imgUrl}
                              style={{ maxWidth: '240px', maxHeight: '180px', borderRadius: 'var(--radius-md)', cursor: 'pointer', border: '1px solid var(--border)' }}
                              onClick={() => window.open(imgUrl, '_blank')}
                            />
                          ))}
                        </div>
                      )}
                    </div>
                  ) : (
                    <div class="msg-bubble assistant-bubble">
                      {thinking && (
                        <ReasoningBubble thinking={thinking} streaming={msg.streaming} />
                      )}
                      {(main || msg.streaming) && (
                        <div class="msg-main-content">
                          {msg.html && !thinking ? (
                            <div class={msg.streaming ? 'msg-streaming' : ''} innerHTML={msg.html}></div>
                          ) : (
                            <span class={msg.streaming ? 'msg-streaming' : ''}>{main}</span>
                          )}
                        </div>
                      )}
                    </div>
                  )}
                  {msg.role === 'user' && msg.content && (
                    <button class="msg-copy" onClick={() => navigator.clipboard?.writeText(msg.content || '').catch(() => {})}>复制</button>
                  )}
                </div>
              )
            })}
          </div>

          {/* Run status */}
          {(props.sending || props.awaitingInteract) && props.runStatusLine && (
            <div class="run-status" role="status" aria-live="polite">
              <span class="run-status-dot"></span>
              <span>{props.runStatusLine}</span>
            </div>
          )}

          {/* Composer */}
          <div class="composer" style={{ position: 'relative' }}>
            {showMentionMenu.value && filteredMentions.value.length > 0 && (
              <div
                class="composer-mention-menu"
                style={{
                  position: 'absolute',
                  bottom: '100%',
                  left: '12px',
                  right: '12px',
                  maxHeight: '240px',
                  overflowY: 'auto',
                  backgroundColor: '#18181B',
                  border: '1px solid rgba(255,255,255,0.12)',
                  borderRadius: '8px',
                  boxShadow: '0 8px 24px rgba(0,0,0,0.5)',
                  zIndex: 100,
                  padding: '4px',
                  marginBottom: '8px',
                }}
              >
                <div style={{ fontSize: '10px', color: '#71717A', padding: '4px 8px', fontWeight: 600, borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
                  添加上下文引用 (↑↓ 选择，Enter / Tab 确认)
                </div>
                {filteredMentions.value.map((item, idx) => {
                  const isSelected = idx === mentionIndex.value
                  return (
                    <div
                      key={item.id}
                      onClick={() => selectMention(item)}
                      onMouseenter={() => { mentionIndex.value = idx }}
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: '8px',
                        padding: '6px 10px',
                        borderRadius: '6px',
                        backgroundColor: isSelected ? 'rgba(56, 189, 248, 0.15)' : 'transparent',
                        color: isSelected ? '#38BDF8' : '#E4E4E7',
                        cursor: 'pointer',
                        fontSize: '12px',
                      }}
                    >
                      <span style={{ fontSize: '14px' }}>{item.icon}</span>
                      <div style={{ display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
                        <span style={{ fontWeight: 600 }}>{item.label}</span>
                        <span style={{ fontSize: '10px', color: '#71717A', whiteSpace: 'nowrap', textOverflow: 'ellipsis', overflow: 'hidden' }}>
                          {item.desc}
                        </span>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
            <PromptSuggestions
              disabled={props.sending || props.awaitingInteract}
              onSelect={(prompt) => {
                inputText.value = prompt
                nextTick(() => textareaEl.value?.focus())
              }}
            />
            {pastedImages.value.length > 0 && (
              <div class="composer-previews" style={{ display: 'flex', gap: '8px', padding: '4px 2px 8px', flexWrap: 'wrap' }}>
                {pastedImages.value.map((imgUrl, idx) => (
                  <div key={imgUrl} style={{ position: 'relative', width: '56px', height: '56px', borderRadius: 'var(--radius-md)', overflow: 'hidden', border: '1px solid var(--border)' }}>
                    <img src={imgUrl} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                    <button
                      onClick={() => pastedImages.value.splice(idx, 1)}
                      style={{ position: 'absolute', top: '2px', right: '2px', width: '16px', height: '16px', borderRadius: '50%', background: 'rgba(0,0,0,0.6)', color: '#fff', border: 'none', fontSize: '10px', display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer', padding: 0 }}
                      title="删除"
                    >✕</button>
                  </div>
                ))}
              </div>
            )}
            <div class="composer-box">
              <textarea
                ref={textareaEl}
                class="composer-input"
                placeholder="输入消息… (Enter 发送, Shift+Enter 换行, 输入 @ 引用文件或上下文)"
                rows={1}
                value={inputText.value}
                onInput={handleInput}
                onKeydown={handleKeydown}
                onPaste={handlePaste}
                disabled={props.awaitingInteract}
              ></textarea>
              <div class="composer-actions">
                <button
                  class="composer-btn"
                  title="添加上下文 (@ 引用文件/代码库/Git)"
                  onClick={() => {
                    if (!showMentionMenu.value) {
                      showMentionMenu.value = true
                      mentionFilter.value = ''
                      mentionStartIndex.value = inputText.value.length
                      inputText.value += '@'
                      nextTick(() => {
                        textareaEl.value?.focus()
                        fetchWorkspaceFiles()
                      })
                    } else {
                      showMentionMenu.value = false
                    }
                  }}
                  style={{ color: showMentionMenu.value ? '#38BDF8' : 'inherit' }}
                >
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/></svg>
                </button>
                {props.sending ? (
                  <div style={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                    {inputText.value.trim() && (
                      <button
                        class="composer-send"
                        onClick={handleSend}
                        title="发送中途纠偏指引 (Enter)"
                        style={{ backgroundColor: '#F59E0B', color: '#18181B', width: 'auto', padding: '0 8px', borderRadius: '4px' }}
                      >
                        <span style={{ fontSize: '11px', fontWeight: 700 }}>⚡ 纠偏</span>
                      </button>
                    )}
                    <button class="composer-send composer-stop" onClick={props.onStop} title="停止">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="3" y="3" width="18" height="18" rx="2"/></svg>
                    </button>
                  </div>
                ) : (
                  <button
                    class="composer-send"
                    onClick={handleSend}
                    disabled={!inputText.value.trim() || props.awaitingInteract}
                    title="发送 (Enter)"
                  >
                    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg>
                  </button>
                )}
              </div>
            </div>
            <div class="composer-hint">Vite + Vue3 TSX · AgentGo</div>
          </div>
        </div>
      )
    }
  },
})
