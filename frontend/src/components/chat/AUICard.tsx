import type { Message } from '../../composables/useChat'

/* ── A2UI Card components ── */
export const AUICard = ({
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
export default AUICard
