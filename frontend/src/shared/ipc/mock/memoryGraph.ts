// 浏览器预览（mock）下的记忆图谱：mock 记忆本身没有关联，这里按固定规则造一张小图，
// 足够看清总览 / 邻域 / 过滤 / 截断的界面形态。真实数据由 Go 侧 memory.Graph 生成。

type Row = Record<string, any>

function links(rows: Row[]) {
  const out: { source: string; target: string; relation: string; weight: number }[] = []
  rows.forEach((m, i) => {
    if (i > 0) out.push({ source: rows[i - 1].id, target: m.id, relation: 'related', weight: 1 })
    if (i > 1) out.push({ source: rows[0].id, target: m.id, relation: i % 3 === 0 ? 'supersedes' : 'related', weight: 1 + (i % 3) })
  })
  return out
}

export function mockMemoryGraph(params: any[], memories: Row[]): any {
  const [scope, modeRaw, centerRaw, depthRaw, kindsRaw, limitRaw] = params
  const mode = String(modeRaw || 'overview')
  const center = String(centerRaw || '')
  const depth = Math.max(1, Math.min(3, Number(depthRaw) || 1))
  const limit = Math.max(1, Math.min(300, Number(limitRaw) || 80))
  const kinds = String(kindsRaw || '').split(',').map(s => s.trim().toLowerCase()).filter(Boolean)
  const rows = memories.filter(m => m.status === 'active')
  const kindOf = new Map(rows.map(m => [m.id, String(m.modality || 'fact').toLowerCase()]))
  const ok = (id: string) => !kinds.length || kinds.includes(kindOf.get(id) || '') || (mode === 'ego' && id === center)
  const edges = links(rows).filter(e => ok(e.source) && ok(e.target))
  const degree = new Map<string, number>()
  for (const e of edges) {
    degree.set(e.source, (degree.get(e.source) || 0) + 1)
    degree.set(e.target, (degree.get(e.target) || 0) + 1)
  }
  let chosen: string[]
  let total: number
  if (mode === 'ego') {
    if (!rows.some(m => m.id === center)) return { success: false, not_found: true, error: '这条记忆不存在，或不在当前范围内', preview: true }
    const seen = new Set([center])
    chosen = [center]
    let frontier = [center]
    let reached = 1
    for (let d = 0; d < depth && frontier.length; d++) {
      const next: string[] = []
      for (const id of frontier) {
        for (const e of edges) {
          const o = e.source === id ? e.target : e.target === id ? e.source : ''
          if (o && !seen.has(o)) { seen.add(o); next.push(o) }
        }
      }
      reached += next.length
      frontier = []
      for (const id of next) if (chosen.length < limit) { chosen.push(id); frontier.push(id) }
    }
    total = reached
  } else {
    const all = [...degree.keys()].sort((a, b) => (degree.get(b)! - degree.get(a)!) || (a < b ? -1 : 1))
    total = all.length
    chosen = all.slice(0, limit)
  }
  const keep = new Set(chosen)
  const nodes = chosen.map(id => {
    const m = rows.find(r => r.id === id)!
    return { id, title: String(m.content || id).split('\n')[0].slice(0, 48), modality: kindOf.get(id) || 'fact', scope: m.scope || 'global', importance: m.importance || 1, canonical: !!m.is_canonical, degree: degree.get(id) || 0 }
  })
  const groups: Record<string, number> = {}
  for (const n of nodes) groups[n.modality] = (groups[n.modality] || 0) + 1
  return {
    success: true, preview: true,
    graph: {
      nodes, edges: edges.filter(e => keep.has(e.source) && keep.has(e.target)),
      meta: {
        mode, scope: String(scope || 'global'), total, returned: nodes.length, truncated: nodes.length < total,
        isolated: mode === 'ego' ? 0 : Math.max(0, rows.filter(m => ok(m.id)).length - degree.size),
        center: mode === 'ego' ? center : undefined, depth: mode === 'ego' ? depth : undefined, groups,
      },
    },
  }
}
