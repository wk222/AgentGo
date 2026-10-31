// 记忆图谱的纯逻辑：分组布局、节点大小、提示文案。不碰 DOM，便于单测。
import type { MemoryGraphData, MemoryGraphEdge, MemoryGraphMeta, MemoryGraphNode } from '../../shared/ipc/types'

export const KIND_LABELS: Record<string, string> = {
  fact: '事实', episode: '经历', reflection: '反思', insight: '洞察', journal: '日志',
}
export const KIND_ORDER = ['fact', 'episode', 'reflection', 'insight', 'journal']
const PALETTE = ['#4f8cff', '#e0873a', '#3fae7a', '#b266d9', '#d9546b', '#8a8f98']

export const kindLabel = (kind: string) => KIND_LABELS[kind] || kind || '其他'

/** 同一种类型永远同一个颜色：已知类型按固定顺序，未知类型按名称取模。 */
export function kindColor(kind: string): string {
  const i = KIND_ORDER.indexOf(kind)
  if (i >= 0) return PALETTE[i % PALETTE.length]
  let h = 0
  for (const ch of kind || '') h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return PALETTE[h % PALETTE.length]
}

export function nodeRadius(degree: number, maxDegree: number): number {
  if (maxDegree <= 0) return 10
  return 10 + 14 * Math.sqrt(Math.max(0, degree) / maxDegree)
}

export interface Placed { x: number; y: number; r: number }
export interface ClusterCaption { kind: string; label: string; count: number; x: number; y: number; r: number }
export interface GraphLayout {
  positions: Record<string, Placed>
  clusters: ClusterCaption[]
  /** 包住所有节点和分组标题的矩形，用来适配视口 */
  bounds: { x: number; y: number; width: number; height: number }
}

const CLUSTER_SPACING = 56
const EGO_RING_GAP = 120
const EGO_NODE_GAP = 8

const byDegreeThenId = (a: MemoryGraphNode, b: MemoryGraphNode) => (b.degree - a.degree) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)

/** 一个分组内：度最大的放中心，其余按圈排开，第 r 圈放 6r 个。 */
function clusterShape(count: number): { rings: number; radius: number } {
  let rings = 0
  let placed = 1
  while (placed < count) { rings++; placed += 6 * rings }
  return { rings, radius: rings * CLUSTER_SPACING + 34 }
}

function placeCluster(members: MemoryGraphNode[], cx: number, cy: number, maxDeg: number, out: Record<string, Placed>) {
  const sorted = [...members].sort(byDegreeThenId)
  let idx = 0
  out[sorted[0].id] = { x: cx, y: cy, r: nodeRadius(sorted[0].degree, maxDeg) }
  idx = 1
  for (let ring = 1; idx < sorted.length; ring++) {
    const cap = Math.min(6 * ring, sorted.length - idx)
    // 每一圈错开半个间隔，避免相邻两圈的点排成放射线
    const phase = ring % 2 ? 0 : Math.PI / (6 * ring)
    for (let k = 0; k < cap; k++) {
      const a = phase + (2 * Math.PI * k) / (6 * ring) - Math.PI / 2
      const n = sorted[idx++]
      out[n.id] = { x: cx + Math.cos(a) * ring * CLUSTER_SPACING, y: cy + Math.sin(a) * ring * CLUSTER_SPACING, r: nodeRadius(n.degree, maxDeg) }
    }
  }
}

function groupByKind(nodes: MemoryGraphNode[]): Map<string, MemoryGraphNode[]> {
  const groups = new Map<string, MemoryGraphNode[]>()
  for (const n of nodes) {
    const k = n.modality || ''
    groups.set(k, [...(groups.get(k) || []), n])
  }
  return groups
}

function orderedKinds(groups: Map<string, MemoryGraphNode[]>): string[] {
  return [...groups.keys()].sort((a, b) => {
    const d = (groups.get(b)!.length - groups.get(a)!.length)
    if (d) return d
    const ia = KIND_ORDER.indexOf(a)
    const ib = KIND_ORDER.indexOf(b)
    if (ia !== ib) return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib)
    return a < b ? -1 : a > b ? 1 : 0
  })
}

function boundsOf(positions: Record<string, Placed>, clusters: ClusterCaption[]) {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
  for (const p of Object.values(positions)) {
    x0 = Math.min(x0, p.x - p.r); y0 = Math.min(y0, p.y - p.r)
    x1 = Math.max(x1, p.x + p.r); y1 = Math.max(y1, p.y + p.r)
  }
  for (const c of clusters) {
    x0 = Math.min(x0, c.x - c.r); y0 = Math.min(y0, c.y - c.r - 22)
    x1 = Math.max(x1, c.x + c.r); y1 = Math.max(y1, c.y + c.r)
  }
  if (!Number.isFinite(x0)) return { x: -100, y: -100, width: 200, height: 200 }
  const pad = 24
  return { x: x0 - pad, y: y0 - pad, width: x1 - x0 + 2 * pad, height: y1 - y0 + 2 * pad }
}

/** 总览：每种类型一组，各组围成一圈；组内同心圈排列。 */
function overviewLayout(nodes: MemoryGraphNode[]): GraphLayout {
  const positions: Record<string, Placed> = {}
  const groups = groupByKind(nodes)
  const kinds = orderedKinds(groups)
  const maxDeg = nodes.reduce((m, n) => Math.max(m, n.degree), 0)
  const shapes = kinds.map(k => clusterShape(groups.get(k)!.length))
  const maxR = shapes.reduce((m, s) => Math.max(m, s.radius), 0)
  const ring = kinds.length < 2 ? 0 : (2 * maxR + 48) / (2 * Math.sin(Math.PI / kinds.length))
  const clusters: ClusterCaption[] = []
  kinds.forEach((k, i) => {
    const a = kinds.length < 2 ? 0 : (2 * Math.PI * i) / kinds.length - Math.PI / 2
    const cx = Math.cos(a) * ring
    const cy = Math.sin(a) * ring
    placeCluster(groups.get(k)!, cx, cy, maxDeg, positions)
    clusters.push({ kind: k, label: kindLabel(k), count: groups.get(k)!.length, x: cx, y: cy, r: shapes[i].radius })
  })
  return { positions, clusters, bounds: boundsOf(positions, clusters) }
}

/** 邻域：中心在原点，按离中心的跳数一圈一圈向外；同一圈内同类型挨着放。 */
function egoLayout(nodes: MemoryGraphNode[], edges: MemoryGraphEdge[], center: string): GraphLayout {
  const positions: Record<string, Placed> = {}
  const maxDeg = nodes.reduce((m, n) => Math.max(m, n.degree), 0)
  const byId = new Map(nodes.map(n => [n.id, n]))
  const adj = new Map<string, string[]>()
  for (const e of edges) {
    if (!byId.has(e.source) || !byId.has(e.target)) continue
    adj.set(e.source, [...(adj.get(e.source) || []), e.target])
    adj.set(e.target, [...(adj.get(e.target) || []), e.source])
  }
  const depth = new Map<string, number>()
  if (byId.has(center)) {
    depth.set(center, 0)
    const queue = [center]
    for (let q = 0; q < queue.length; q++) {
      for (const nb of adj.get(queue[q]) || []) {
        if (!depth.has(nb)) { depth.set(nb, depth.get(queue[q])! + 1); queue.push(nb) }
      }
    }
  }
  let outer = 0
  for (const d of depth.values()) outer = Math.max(outer, d)
  const levels = new Map<number, MemoryGraphNode[]>()
  for (const n of nodes) {
    // 不会发生：结果里的节点都经边连到中心。保险起见放最外圈，而不是丢掉。
    const d = depth.has(n.id) ? depth.get(n.id)! : outer + 1
    levels.set(d, [...(levels.get(d) || []), n])
  }
  let prev = 0
  for (const d of [...levels.keys()].sort((a, b) => a - b)) {
    const members = levels.get(d)!
    if (d === 0) {
      for (const n of members) positions[n.id] = { x: 0, y: 0, r: nodeRadius(n.degree, maxDeg) + 4 }
      continue
    }
    const kindRank = (k: string) => { const i = KIND_ORDER.indexOf(k); return i < 0 ? 99 : i }
    members.sort((a, b) => (kindRank(a.modality) - kindRank(b.modality)) || byDegreeThenId(a, b))
    // 相邻两点的直线距离（弦长）要容得下这一圈里最大节点的直径加间隔，否则大节点挨着会重叠。
    const widest = members.reduce((w, n) => Math.max(w, nodeRadius(n.degree, maxDeg)), 0)
    const need = members.length > 1 ? (2 * widest + EGO_NODE_GAP) / (2 * Math.sin(Math.PI / members.length)) : 0
    const radius = Math.max(prev + EGO_RING_GAP, need)
    members.forEach((n, i) => {
      const a = (2 * Math.PI * i) / members.length - Math.PI / 2 + (d % 2 ? 0 : Math.PI / members.length)
      positions[n.id] = { x: Math.cos(a) * radius, y: Math.sin(a) * radius, r: nodeRadius(n.degree, maxDeg) }
    })
    prev = radius
  }
  return { positions, clusters: [], bounds: boundsOf(positions, []) }
}

export function layoutGraph(data: Pick<MemoryGraphData, 'nodes' | 'edges'> & { meta?: Partial<MemoryGraphMeta> }): GraphLayout {
  const nodes = data.nodes || []
  if (!nodes.length) return { positions: {}, clusters: [], bounds: boundsOf({}, []) }
  if (data.meta?.mode === 'ego' && data.meta.center) return egoLayout(nodes, data.edges || [], data.meta.center)
  return overviewLayout(nodes)
}

export interface Viewport { x: number; y: number; k: number }

/** 让整张图恰好放进 width×height 的画布，最多放大到 1.4 倍。 */
export function fitViewport(bounds: GraphLayout['bounds'], width: number, height: number): Viewport {
  const k = Math.max(0.05, Math.min(1.4, Math.min(width / bounds.width, height / bounds.height)))
  return { k, x: width / 2 - (bounds.x + bounds.width / 2) * k, y: height / 2 - (bounds.y + bounds.height / 2) * k }
}

/** 以屏幕点 (px,py) 为不动点缩放；缩放范围限制在 0.1–4。 */
export function zoomAt(v: Viewport, px: number, py: number, factor: number): Viewport {
  const k = Math.max(0.1, Math.min(4, v.k * factor))
  const f = k / v.k
  return { k, x: px - (px - v.x) * f, y: py - (py - v.y) * f }
}

export function neighborIds(edges: MemoryGraphEdge[], id: string): Set<string> {
  const out = new Set<string>()
  for (const e of edges) {
    if (e.source === id) out.add(e.target)
    else if (e.target === id) out.add(e.source)
  }
  return out
}

/** 连线样式：取代关系画虚线，其余实线；粗细随权重（1 起步，封顶 4px）。 */
export function edgeStyle(e: MemoryGraphEdge): { dash: string; width: number } {
  return { dash: e.relation === 'supersedes' ? '6 4' : '', width: Math.max(1, Math.min(4, 0.8 + (e.weight || 1) * 0.7)) }
}

export function graphNotice(meta: MemoryGraphMeta | undefined | null): string {
  if (!meta) return ''
  const parts: string[] = []
  if (meta.mode === 'ego') {
    parts.push(meta.truncated ? `邻域内至少有 ${meta.total} 条记忆，只显示最近、最强关联的 ${meta.returned} 条。` : `邻域内共 ${meta.returned} 条记忆。`)
  } else if (meta.total === 0) {
    parts.push('还没有任何带关联的记忆。')
  } else {
    parts.push(meta.truncated ? `共 ${meta.total} 条有关联的记忆，显示关联最多的 ${meta.returned} 条。` : `共 ${meta.returned} 条有关联的记忆。`)
  }
  if (meta.mode !== 'ego' && meta.isolated > 0) parts.push(`另有 ${meta.isolated} 条没有任何关联，不会画在图里。`)
  return parts.join('')
}

/** 空结果时给的原因，区分「没有数据」和「被过滤掉了」。 */
export function emptyHint(meta: MemoryGraphMeta | undefined | null, filtered: boolean): string {
  if (filtered) return '当前类型过滤下没有带关联的记忆。取消过滤或换一个范围试试。'
  if (meta && meta.isolated > 0) return `有 ${meta.isolated} 条记忆，但它们之间还没有建立关联。`
  return '这个范围里还没有记忆。'
}

// ---- 节点标题 ----
// SVG 文字不会自己避让，几个靠得近的节点标题会叠成一团。这里在「世界坐标」里估算每个标题的
// 占位（文字随图一起缩放，所以与缩放比例无关），靠近的节点依次放不下就缩短，再放不下就不显示；
// 选中、悬停、邻域中心的标题优先，并且一定显示。

const LABEL_FONT = 11
const LABEL_FULL = 120
const LABEL_SHORT = 52
const LABEL_PAD = 3

/** 标题的大致宽度（与字号同单位）：全角字符占一个字号，其余约 0.55 个。 */
export function labelWidth(text: string): number {
  let w = 0
  for (const ch of text) w += (ch.codePointAt(0) || 0) > 0x2e7f ? LABEL_FONT : LABEL_FONT * 0.55
  return w
}

/** 截到不超过 maxWidth，超出处以省略号结尾。 */
export function clipLabel(text: string, maxWidth: number): string {
  if (labelWidth(text) <= maxWidth) return text
  let out = ''
  for (const ch of text) {
    if (labelWidth(out + ch + '…') > maxWidth) break
    out += ch
  }
  return out + '…'
}

export interface LabelItem { id: string; title: string }

/** 返回每个节点实际显示的标题（不显示的不在结果里）。always 里的节点一定显示。 */
export function placeLabels(items: LabelItem[], positions: Record<string, Placed>, always: Set<string>): Record<string, string> {
  type Box = { x0: number; x1: number; y0: number; y1: number }
  const placed: Box[] = []
  const out: Record<string, string> = {}
  const hit = (b: Box) => placed.some(o => b.x0 < o.x1 && b.x1 > o.x0 && b.y0 < o.y1 && b.y1 > o.y0)
  const boxOf = (p: Placed, text: string): Box => {
    const w = labelWidth(text) / 2 + LABEL_PAD
    return { x0: p.x - w, x1: p.x + w, y0: p.y + p.r + 2, y1: p.y + p.r + 2 + LABEL_FONT + 2 * LABEL_PAD }
  }
  const order = items
    .filter(it => positions[it.id])
    .sort((a, b) => {
      const pa = always.has(a.id) ? 1 : 0
      const pb = always.has(b.id) ? 1 : 0
      return (pb - pa) || (positions[b.id].r - positions[a.id].r) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
    })
  for (const it of order) {
    const p = positions[it.id]
    const must = always.has(it.id)
    for (const width of [LABEL_FULL, LABEL_SHORT]) {
      const text = clipLabel(it.title, width)
      if (!text) break
      const box = boxOf(p, text)
      if (must || !hit(box)) {
        out[it.id] = text
        placed.push(box)
        break
      }
    }
  }
  return out
}