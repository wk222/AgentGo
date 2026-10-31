// 浏览器预览（mock）下的记忆树：把当前 mock 记忆按类型归成一层分支，足够看清界面形态。
// 真实运行时的树由 Go 侧 internal/memory 生成，这里不模拟模型写摘要。

type Row = Record<string, any>

const leaf = (m: Row) => ({
  id: `mt_leaf_${m.id}`, label: m.modality || 'memory', summary: String(m.content || '').slice(0, 80), count: 1, level: 0,
  complete: true, record_id: m.id, child_count: 0,
  record: {
    modality: m.modality, scope: m.scope, kind: m.modality, canonical: !!m.is_canonical,
    importance: m.importance || 0, created_at: m.created_at || 0, content: m.content,
  },
})

function build(rows: Row[]) {
  const groups = new Map<string, Row[]>()
  for (const m of rows) {
    const key = m.modality || 'memory'
    groups.set(key, [...(groups.get(key) || []), m])
  }
  const branches = [...groups.entries()].map(([label, items]) => ({
    id: `mt_branch_${label}`, label, summary: items.map(i => i.content).join(' / ').slice(0, 120),
    count: items.length, level: 1, complete: items.length > 1, child_count: items.length,
    children: items.map(leaf),
  }))
  const root = {
    id: 'mt_root', label: 'Memory themes', summary: branches.map(b => b.label).join(', '), count: rows.length,
    level: 2, complete: false, child_count: branches.length, children: branches.map(b => ({ ...b, children: undefined })),
  }
  return { branches, root }
}

/** 返回 undefined 表示该方法不归记忆树处理。 */
export function mockMemoryTree(method: string, params: any[], memories: Row[]): any {
  const rows = memories.filter(m => m.status === 'active')
  const { branches, root } = build(rows)
  const missing = { success: false, error: 'node is not in the current memory scope' }

  const find = (id: string): any => {
    if (id === root.id) return root
    for (const b of branches) {
      if (b.id === id) return b
      const hit = b.children.find(c => c.id === id)
      if (hit) return hit
    }
    return undefined
  }

  switch (method) {
    case 'GetMemoryTree':
    case 'RefreshMemoryTree': {
      if (method === 'GetMemoryTree' && params[1]) {
        const node = find(String(params[1]))
        return node ? { success: true, node, preview: true } : missing
      }
      return {
        success: true, hierarchy: true, preview: true,
        overview: {
          scope: String(params[0] || 'global'), ready: rows.length > 0, stale: false,
          reason: rows.length ? undefined : 'empty', refreshing: false,
          records: rows.length, covered: rows.length, nodes: rows.length + branches.length + 1, branches: branches.length + 1,
          summarized: branches.filter(b => b.complete).length, levels: 3, fanout: 8, node_tokens: 128,
          root: rows.length ? root : undefined,
        },
      }
    }
    case 'LocateMemoryTreeNode': {
      const id = String(params[1] || '')
      const node = find(id)
      if (!node) return missing
      if (id === root.id) return { success: true, chain: [root], preview: true }
      const parent = branches.find(b => b.id === id || b.children.some(c => c.id === id))
      const chain = [root, ...(parent && parent.id !== id ? [parent] : []), node]
      return { success: true, chain, preview: true }
    }
    case 'PreviewMemoryContext': {
      // 预览模式下假装模型读到了第一个分支和它旁边的一条记忆，让「模型视角」区块有东西可看。
      const seen = [branches[0], branches[1]?.children[0]].filter(Boolean).map(n => ({ ...n, children: undefined }))
      const prompt = seen.map(n => `[node=${n.id}; n=${n.count}] ${n.summary}`).join('\n')
      return {
        success: true, scope: String(params[0] || 'global'), query: String(params[1] || ''), budget: 1000,
        tokens: Math.ceil(prompt.length / 2), hierarchy: true, prompt, node_ids: seen.map(n => n.id), nodes: seen, preview: true,
      }
    }
    default:
      return undefined
  }
}
