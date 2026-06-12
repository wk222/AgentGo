import { defineComponent, ref, nextTick, watch, PropType } from 'vue'
import type { Message, Session } from './composables/useChat'
import { sessionTitle } from './composables/useChat'
import { wailsCall } from './wails'

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

    const handleSend = () => {
      const text = inputText.value.trim()
      if (!text && pastedImages.value.length === 0) return
      if (props.sending) return
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

    const handleKeydown = (e: KeyboardEvent) => {
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
                    <div class="msg-approval">
                      <div class="msg-approval-label">需要批准</div>
                      <div class="msg-approval-prompt">{msg.content || '需要您的批准才能继续'}</div>
                      <div class="msg-approval-actions">
                        <button class="msg-approve-btn ok" onClick={() => props.onApprove(msg.approval_id || '')}>批准</button>
                        <button class="msg-approve-btn no" onClick={() => props.onReject(msg.approval_id || '')}>拒绝</button>
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

              /* Text message */
              return (
                <div key={msg._key || index} class={['msg-row', msg.role]}>
                  {msg.role === 'user' ? (
                    <div class="msg-bubble">
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
                    <div class="msg-bubble">
                      {msg.html
                        ? <div class={msg.streaming ? 'msg-streaming' : ''} innerHTML={msg.html}></div>
                        : <span class={msg.streaming ? 'msg-streaming' : ''}>{msg.content}</span>
                      }
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
            <div class="run-status">
              <span class="run-status-dot"></span>
              <span>{props.runStatusLine}</span>
            </div>
          )}

          {/* Composer */}
          <div class="composer">
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
                placeholder="输入消息… (Enter 发送, Shift+Enter 换行)"
                rows={1}
                value={inputText.value}
                onInput={(e: Event) => { inputText.value = (e.target as HTMLTextAreaElement).value; autoResize(e) }}
                onKeydown={handleKeydown}
                onPaste={handlePaste}
                disabled={props.awaitingInteract}
              ></textarea>
              <div class="composer-actions">
                <button class="composer-btn" title="附件（暂未开放）" disabled>
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/></svg>
                </button>
                {props.sending ? (
                  <button class="composer-send composer-stop" onClick={props.onStop} title="停止">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="3" y="3" width="18" height="18" rx="2"/></svg>
                  </button>
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
