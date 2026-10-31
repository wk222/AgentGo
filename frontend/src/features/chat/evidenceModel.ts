import type { Message } from '../../stores/useChat'

export interface EvidenceExcerpt {
  id: string
  kind: 'file' | 'web'
  source: string
  revision: string
  start: number
  end: number
  text: string
  spans: Array<{ page?: number; sheet?: string; row_start?: number; row_end?: number }>
}

const citationID = /^ev_[a-f0-9]{32}$/

function excerpt(value: any, tool: string): EvidenceExcerpt | undefined {
  const c = value?.citation
  if (!c || !citationID.test(c.id) || !['file', 'web'].includes(c.kind) ||
      typeof c.source !== 'string' || !c.source || !/^[a-f0-9]{64}$/.test(c.revision) ||
      !Number.isSafeInteger(c.start) || !Number.isSafeInteger(c.end) || c.start < 0 ||
      typeof value.text !== 'string' || !value.text || [...value.text].length !== c.end - c.start) return
  if (tool === 'parse_file' && (c.kind !== 'file' || c.source !== value.source?.path || c.revision !== value.source?.revision || c.start !== value.offset)) return
  if (tool === 'web_fetch' && (c.kind !== 'web' || value.status !== 'ok' || c.source !== value.url || c.revision !== value.revision || c.start !== value.offset)) return
  // No filesystem or network action is derived from source strings.
  return { ...c, text: value.text, spans: Array.isArray(c.source_spans) ? c.source_spans : [] }
}

/** Only actual successful tool results preceding this answer in its user turn. */
export function evidenceForMessage(messages: Message[], index: number): Map<string, EvidenceExcerpt> {
  const records = new Map<string, EvidenceExcerpt>()
  let start = index - 1
  while (start >= 0 && messages[start].role !== 'user') start--
  for (let i = start + 1; i < index; i++) {
    const m = messages[i]
    if (m.type !== 'tool_call' || m.status !== 'success' || !['parse_file', 'web_fetch', 'read_evidence'].includes(m.toolName || '')) continue
    try {
      const data = JSON.parse(m.content || '')
      const values = m.toolName === 'web_fetch' ? data.results : [data]
      if (!Array.isArray(values)) continue
      for (const value of values) {
        const record = excerpt(value, m.toolName!)
        if (record && !records.has(record.id)) records.set(record.id, record)
      }
    } catch { /* Truncated or legacy tool results cannot authorize a citation. */ }
  }
  return records
}

export function evidenceLocation(record: EvidenceExcerpt): string {
  const locations = new Set<string>()
  for (const span of record.spans) {
    if (Number.isSafeInteger(span.page) && span.page! > 0) locations.add(`第 ${span.page} 页`)
    else if (typeof span.sheet === 'string' && Number.isSafeInteger(span.row_start) && span.row_start! > 0) {
      const end = span.row_end && span.row_end > span.row_start! ? `–${span.row_end}` : ''
      locations.add(`${span.sheet} · 第 ${span.row_start}${end} 行`)
    }
    if (locations.size === 8) break
  }
  return [...locations].join('；') || `解析文本字符 ${record.start}–${record.end}`
}

/** Operate on text nodes so code, links and HTML attributes never become buttons. */
export function decorateEvidenceHTML(html: string, records: Map<string, EvidenceExcerpt>): string {
  const doc = new DOMParser().parseFromString(html, 'text/html')
  doc.querySelectorAll('[data-evidence-id]').forEach(el => el.removeAttribute('data-evidence-id'))
  const walker = doc.createTreeWalker(doc.body, NodeFilter.SHOW_TEXT)
  const nodes: Text[] = []
  while (walker.nextNode()) nodes.push(walker.currentNode as Text)
  for (const node of nodes) {
    if (node.parentElement?.closest('pre,code,a,button,script,style')) continue
    const pattern = /\[\[(ev_[a-f0-9]{32})\]\]/g
    const text = node.textContent || ''
    const matches = [...text.matchAll(pattern)]
    if (!matches.length) continue
    const fragment = doc.createDocumentFragment()
    let cursor = 0
    for (const match of matches) {
      fragment.append(doc.createTextNode(text.slice(cursor, match.index)))
      const record = records.get(match[1])
      const el = doc.createElement(record ? 'button' : 'span')
      el.className = record ? 'evidence-link' : 'evidence-unavailable'
      el.textContent = record ? '[来源]' : '[来源不可用]'
      el.title = record ? `${record.source} · ${evidenceLocation(record)}` : '本轮没有对应的完整来源记录'
      if (record) { el.setAttribute('type', 'button'); el.setAttribute('data-evidence-id', record.id) }
      fragment.append(el)
      cursor = match.index! + match[0].length
    }
    fragment.append(doc.createTextNode(text.slice(cursor)))
    node.replaceWith(fragment)
  }
  return doc.body.innerHTML
}
