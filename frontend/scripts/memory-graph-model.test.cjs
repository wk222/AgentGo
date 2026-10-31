// 记忆图谱布局与提示文案的纯逻辑单测：与 memory-tree-model.test.cjs 相同的做法，转译 TS 后直接加载。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/features/memory/memoryGraphModel.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const m = mod.exports

const node = (id, modality, degree) => ({ id, title: id, modality, scope: 'global', importance: 1, degree })
const many = (prefix, modality, n, degree = 1) => Array.from({ length: n }, (_, i) => node(`${prefix}${i}`, modality, degree + (n - i)))
const dist = (a, b) => Math.hypot(a.x - b.x, a.y - b.y)

function assertNoOverlap(layout) {
  const ids = Object.keys(layout.positions)
  for (let i = 0; i < ids.length; i++) {
    for (let j = i + 1; j < ids.length; j++) {
      const a = layout.positions[ids[i]]
      const b = layout.positions[ids[j]]
      assert.ok(dist(a, b) >= a.r + b.r, `${ids[i]} and ${ids[j]} overlap (${dist(a, b).toFixed(1)} < ${(a.r + b.r).toFixed(1)})`)
    }
  }
}

function assertInsideBounds(layout) {
  const { x, y, width, height } = layout.bounds
  for (const [id, p] of Object.entries(layout.positions)) {
    assert.ok(p.x - p.r >= x && p.x + p.r <= x + width && p.y - p.r >= y && p.y + p.r <= y + height, `${id} outside bounds`)
  }
}

test('类型颜色：已知类型固定且互不相同，未知类型稳定', () => {
  const colors = m.KIND_ORDER.map(m.kindColor)
  assert.equal(new Set(colors).size, colors.length)
  assert.equal(m.kindColor('fact'), m.kindColor('fact'))
  assert.equal(m.kindColor('weird-kind'), m.kindColor('weird-kind'))
  assert.match(m.kindColor('weird-kind'), /^#[0-9a-f]{6}$/)
})

test('类型名：中文名，未知类型原样显示，空类型显示其他', () => {
  assert.equal(m.kindLabel('fact'), '事实')
  assert.equal(m.kindLabel('constraint'), 'constraint')
  assert.equal(m.kindLabel(''), '其他')
})

test('节点半径：度越大越大，范围 10–24，最大度为 0 时不出 NaN', () => {
  assert.equal(m.nodeRadius(0, 0), 10)
  assert.equal(m.nodeRadius(0, 10), 10)
  assert.equal(m.nodeRadius(10, 10), 24)
  assert.ok(m.nodeRadius(5, 10) > m.nodeRadius(1, 10))
  assert.ok(Number.isFinite(m.nodeRadius(-3, 10)))
})

test('总览布局：每个节点只放一次，任意两点不重叠，都在边界内', () => {
  const nodes = [...many('f', 'fact', 60), ...many('e', 'episode', 7), ...many('r', 'reflection', 1)]
  const layout = m.layoutGraph({ nodes, edges: [], meta: { mode: 'overview' } })
  assert.equal(Object.keys(layout.positions).length, nodes.length)
  for (const p of Object.values(layout.positions)) assert.ok(Number.isFinite(p.x) && Number.isFinite(p.y) && p.r > 0)
  assertNoOverlap(layout)
  assertInsideBounds(layout)
})

test('总览布局：分组标题按数量排序并带数量，各组圈不相交', () => {
  const nodes = [...many('e', 'episode', 7), ...many('f', 'fact', 60), ...many('j', 'journal', 7)]
  const { clusters } = m.layoutGraph({ nodes, edges: [], meta: { mode: 'overview' } })
  assert.deepEqual(clusters.map(c => [c.kind, c.count]), [['fact', 60], ['episode', 7], ['journal', 7]])
  assert.equal(clusters[0].label, '事实')
  for (let i = 0; i < clusters.length; i++) {
    for (let j = i + 1; j < clusters.length; j++) {
      assert.ok(dist(clusters[i], clusters[j]) >= clusters[i].r + clusters[j].r, `${clusters[i].kind}/${clusters[j].kind} 组圈相交`)
    }
  }
})

test('总览布局：每组里度最大的节点放在该组中心', () => {
  const nodes = [node('small', 'fact', 1), node('hub', 'fact', 9), node('mid', 'fact', 4), node('x', 'episode', 2)]
  const layout = m.layoutGraph({ nodes, edges: [], meta: { mode: 'overview' } })
  const cluster = layout.clusters.find(c => c.kind === 'fact')
  assert.deepEqual([layout.positions.hub.x, layout.positions.hub.y], [cluster.x, cluster.y])
})

test('总览布局：只有一种类型时放在原点；度相同按 id 排，结果可重复', () => {
  const nodes = [node('b', 'fact', 2), node('a', 'fact', 2), node('c', 'fact', 2)]
  const one = m.layoutGraph({ nodes, edges: [], meta: { mode: 'overview' } })
  assert.deepEqual([one.positions.a.x, one.positions.a.y], [0, 0])
  const again = m.layoutGraph({ nodes: [...nodes].reverse(), edges: [], meta: { mode: 'overview' } })
  assert.deepEqual(again.positions, one.positions)
})

test('邻域布局：中心在原点，离得越远圈越大，圈内不重叠', () => {
  // c — a1..a40（一跳）；a1 — b1..b5（两跳）
  const center = node('c', 'fact', 40)
  const ring1 = many('a', 'episode', 40)
  const ring2 = many('b', 'fact', 5)
  const edges = [
    ...ring1.map(n => ({ source: 'c', target: n.id, weight: 1 })),
    ...ring2.map(n => ({ source: 'a0', target: n.id, weight: 1 })),
  ]
  const layout = m.layoutGraph({ nodes: [center, ...ring1, ...ring2], edges, meta: { mode: 'ego', center: 'c' } })
  assert.deepEqual([layout.positions.c.x, layout.positions.c.y], [0, 0])
  const r1 = Math.hypot(layout.positions.a3.x, layout.positions.a3.y)
  const r2 = Math.hypot(layout.positions.b2.x, layout.positions.b2.y)
  assert.ok(r1 > 0 && r2 > r1 + 60, `两圈间距不够：${r1.toFixed(0)} → ${r2.toFixed(0)}`)
  for (const n of ring1) assert.ok(Math.abs(Math.hypot(layout.positions[n.id].x, layout.positions[n.id].y) - r1) < 1e-6)
  assertNoOverlap(layout)
  assertInsideBounds(layout)
  assert.deepEqual(layout.clusters, [])
})

test('邻域布局：与中心不连通的节点也放在最外圈，不丢', () => {
  const layout = m.layoutGraph({
    nodes: [node('c', 'fact', 1), node('a', 'fact', 1), node('orphan', 'fact', 1)],
    edges: [{ source: 'c', target: 'a', weight: 1 }], meta: { mode: 'ego', center: 'c' },
  })
  assert.equal(Object.keys(layout.positions).length, 3)
  assert.ok(Math.hypot(layout.positions.orphan.x, layout.positions.orphan.y) > Math.hypot(layout.positions.a.x, layout.positions.a.y))
})

test('空图：边界有限，不出 NaN', () => {
  const layout = m.layoutGraph({ nodes: [], edges: [], meta: { mode: 'overview' } })
  assert.deepEqual(layout.positions, {})
  for (const v of Object.values(layout.bounds)) assert.ok(Number.isFinite(v))
})

test('适配视口：整张图落在画布内，小图最多放大到 1.4 倍', () => {
  const big = { x: -1000, y: -500, width: 2000, height: 1000 }
  const v = m.fitViewport(big, 800, 600)
  assert.ok(v.k <= 0.4 + 1e-9)
  assert.ok(big.x * v.k + v.x >= -1e-6 && (big.x + big.width) * v.k + v.x <= 800 + 1e-6)
  assert.ok(big.y * v.k + v.y >= -1e-6 && (big.y + big.height) * v.k + v.y <= 600 + 1e-6)
  assert.equal(m.fitViewport({ x: 0, y: 0, width: 10, height: 10 }, 800, 600).k, 1.4)
})

test('缩放：鼠标位置不动，倍数限制在 0.1–4', () => {
  const v0 = { x: 30, y: 40, k: 1 }
  const v1 = m.zoomAt(v0, 200, 150, 2)
  assert.equal(v1.k, 2)
  // 屏幕点 (200,150) 对应的图上坐标缩放前后相同
  assert.ok(Math.abs((200 - v0.x) / v0.k - (200 - v1.x) / v1.k) < 1e-9)
  assert.ok(Math.abs((150 - v0.y) / v0.k - (150 - v1.y) / v1.k) < 1e-9)
  assert.equal(m.zoomAt({ x: 0, y: 0, k: 3.9 }, 0, 0, 10).k, 4)
  assert.equal(m.zoomAt({ x: 0, y: 0, k: 0.11 }, 0, 0, 0.01).k, 0.1)
})

test('邻居与连线样式', () => {
  const edges = [{ source: 'a', target: 'b', weight: 1 }, { source: 'c', target: 'a', weight: 1 }, { source: 'x', target: 'y', weight: 1 }]
  assert.deepEqual([...m.neighborIds(edges, 'a')].sort(), ['b', 'c'])
  assert.equal(m.neighborIds(edges, 'zzz').size, 0)
  assert.equal(m.edgeStyle({ source: 'a', target: 'b', relation: 'supersedes', weight: 1 }).dash, '6 4')
  assert.equal(m.edgeStyle({ source: 'a', target: 'b', relation: 'related', weight: 1 }).dash, '')
  assert.equal(m.edgeStyle({ source: 'a', target: 'b', weight: 100 }).width, 4)
  assert.equal(m.edgeStyle({ source: 'a', target: 'b', weight: 0 }).width, 1.5)
  assert.ok(m.edgeStyle({ source: 'a', target: 'b', weight: -9 }).width >= 1)
})

test('提示文案：总览、截断、孤立、邻域、空', () => {
  const base = { scope: 'global', groups: {} }
  assert.equal(m.graphNotice(null), '')
  assert.match(m.graphNotice({ ...base, mode: 'overview', total: 213, returned: 80, truncated: true, isolated: 12 }), /共 213 条.*显示关联最多的 80 条.*另有 12 条没有任何关联/)
  assert.equal(m.graphNotice({ ...base, mode: 'overview', total: 5, returned: 5, truncated: false, isolated: 0 }), '共 5 条有关联的记忆。')
  assert.equal(m.graphNotice({ ...base, mode: 'overview', total: 0, returned: 0, truncated: false, isolated: 3 }).startsWith('还没有任何带关联的记忆。'), true)
  assert.match(m.graphNotice({ ...base, mode: 'ego', total: 9, returned: 4, truncated: true, isolated: 0 }), /至少有 9 条.*4 条/)
  assert.equal(m.graphNotice({ ...base, mode: 'ego', total: 4, returned: 4, truncated: false, isolated: 7 }), '邻域内共 4 条记忆。')
  assert.match(m.emptyHint(null, true), /过滤/)
  assert.match(m.emptyHint({ ...base, mode: 'overview', total: 0, returned: 0, truncated: false, isolated: 6 }, false), /6 条记忆.*还没有建立关联/)
  assert.equal(m.emptyHint(null, false), '这个范围里还没有记忆。')
})

test('标题宽度：全角字符比半角宽；截断后不超宽且以省略号结尾，短标题原样返回', () => {
  assert.ok(m.labelWidth('记忆图谱') > m.labelWidth('abcd'))
  assert.equal(m.clipLabel('short', 200), 'short')
  const cut = m.clipLabel('这是一个非常非常长的记忆标题需要被截断显示', 60)
  assert.ok(cut.endsWith('…'))
  assert.ok(m.labelWidth(cut) <= 60, `${cut} is ${m.labelWidth(cut)} wide`)
  assert.ok(m.clipLabel('abcdefghij', 1).length >= 1)
})

function boxOfLabel(p, text) {
  const w = m.labelWidth(text) / 2
  return { x0: p.x - w, x1: p.x + w, y0: p.y + p.r, y1: p.y + p.r + 11 }
}
const boxesOverlap = (a, b) => a.x0 < b.x1 && a.x1 > b.x0 && a.y0 < b.y1 && a.y1 > b.y0

test('标题避让：靠得近的节点不会叠字；远的节点都显示全称', () => {
  const items = [
    { id: 'a', title: '优先使用 Eino/compose 能力减少自研组件' },
    { id: 'b', title: '窄屏下需要重点检查 topbar 与右栏挤压' },
    { id: 'c', title: '用户希望右侧 Workspace 成为入口' },
  ]
  const pos = { a: { x: 0, y: 0, r: 14 }, b: { x: 70, y: 0, r: 14 }, c: { x: 140, y: 0, r: 14 } }
  const labels = m.placeLabels(items, pos, new Set())
  const ids = Object.keys(labels)
  for (let i = 0; i < ids.length; i++) {
    for (let j = i + 1; j < ids.length; j++) {
      assert.ok(!boxesOverlap(boxOfLabel(pos[ids[i]], labels[ids[i]]), boxOfLabel(pos[ids[j]], labels[ids[j]])), `${ids[i]} and ${ids[j]} labels overlap`)
    }
  }
  assert.ok(ids.length >= 1, 'at least one label is shown')

  const far = { a: { x: 0, y: 0, r: 14 }, b: { x: 600, y: 0, r: 14 }, c: { x: 1200, y: 0, r: 14 } }
  const all = m.placeLabels(items, far, new Set())
  assert.equal(Object.keys(all).length, 3)
  for (const it of items) assert.equal(all[it.id], m.clipLabel(it.title, 120))
})

test('标题避让：选中的节点一定显示，并且先占位；其余让它', () => {
  const items = [{ id: 'big', title: '较大的节点标题标题标题' }, { id: 'sel', title: '被选中的节点标题标题' }]
  const pos = { big: { x: 0, y: 0, r: 24 }, sel: { x: 10, y: 4, r: 10 } }
  const labels = m.placeLabels(items, pos, new Set(['sel']))
  assert.equal(labels.sel, m.clipLabel('被选中的节点标题标题', 120))
  assert.ok(!labels.big || !boxesOverlap(boxOfLabel(pos.big, labels.big), boxOfLabel(pos.sel, labels.sel)))
})

test('标题避让：结果与输入顺序无关，没有位置的节点被忽略', () => {
  const items = Array.from({ length: 12 }, (_, i) => ({ id: `n${i}`, title: `记忆节点第${i}号的标题` }))
  const pos = {}
  items.forEach((it, i) => { pos[it.id] = { x: (i % 4) * 40, y: Math.floor(i / 4) * 30, r: 10 + (i % 3) } })
  const a = m.placeLabels(items, pos, new Set())
  const b = m.placeLabels([...items].reverse(), pos, new Set())
  assert.deepEqual(a, b)
  const withGhost = m.placeLabels([...items, { id: 'ghost', title: '没有位置' }], pos, new Set())
  assert.equal(withGhost.ghost, undefined)
})

test('标题避让：放不下全称时先缩短再放，而不是直接丢掉', () => {
  const items = [{ id: 'a', title: '一二三四五六七八九十一二' }, { id: 'b', title: '壹贰叁肆伍陆柒捌玖拾壹贰' }]
  const pos = { a: { x: 0, y: 0, r: 20 }, b: { x: 100, y: 0, r: 12 } }
  const labels = m.placeLabels(items, pos, new Set())
  assert.equal(labels.a, m.clipLabel(items[0].title, 120))
  assert.ok(labels.b, 'the second label is shortened, not dropped')
  assert.ok(labels.b.endsWith('…') && m.labelWidth(labels.b) <= 52, labels.b)
  assert.ok(!boxesOverlap(boxOfLabel(pos.a, labels.a), boxOfLabel(pos.b, labels.b)))
})

test('标题避让：选中和悬停的节点即使互相压住也都显示', () => {
  const items = [{ id: 'x', title: '被选中的节点' }, { id: 'y', title: '被悬停的节点' }]
  const pos = { x: { x: 0, y: 0, r: 12 }, y: { x: 5, y: 2, r: 12 } }
  const labels = m.placeLabels(items, pos, new Set(['x', 'y']))
  assert.ok(labels.x && labels.y)
})