// 记忆树视图的纯逻辑单测：与 layout.test.cjs 相同的做法，转译 TS 后直接加载。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/features/memory/memoryTreeModel.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const m = mod.exports

const overview = (extra = {}) => ({
  scope: 's', ready: true, stale: false, refreshing: false, records: 24, covered: 24,
  nodes: 31, branches: 7, summarized: 7, levels: 3, fanout: 8, node_tokens: 128, ...extra,
})

test('标题：根、叶子、主题、未知类型', () => {
  assert.equal(m.nodeTitle({ label: 'Memory themes' }), '记忆总览')
  assert.equal(m.nodeTitle({ label: 'fact', record_id: 'm1' }), '事实')
  assert.equal(m.nodeTitle({ label: 'weird', record_id: 'm1' }), 'weird')
  assert.equal(m.nodeTitle({ label: '报表口径' }), '报表口径')
  assert.equal(m.nodeTitle({ label: '' }), '未命名主题')
})

test('条形比例：按父节点占比，最少 4%，异常输入不炸', () => {
  assert.equal(m.barPercent(8, 24), 33)
  assert.equal(m.barPercent(24, 24), 100)
  assert.equal(m.barPercent(1, 1000), 4)
  assert.equal(m.barPercent(30, 24), 100)
  assert.equal(m.barPercent(0, 24), 4)
  assert.equal(m.barPercent(5, 0), 4)
  assert.equal(m.barPercent(NaN, 5), 4)
})

test('状态横幅覆盖所有状态，并区分「开关关闭」', () => {
  assert.equal(m.treeNotice(null, true), null)
  assert.match(m.treeNotice(overview({ ready: false, reason: 'empty', records: 0, covered: 0 }), true).text, /还没有记忆/)
  assert.match(m.treeNotice(overview({ ready: false, reason: 'not_built' }), true).text, /还没建立/)
  assert.equal(m.treeNotice(overview({ reason: 'stale', stale: true }), true).tone, 'warn')
  assert.match(m.treeNotice(overview({ refreshing: true }), true).text, /后台/)
  assert.match(m.treeNotice(overview({ summarized: 3 }), true).text, /3\/7/)
  const ok = m.treeNotice(overview(), true)
  assert.equal(ok.tone, 'ok')
  // 开关关闭时，即使树是新的也要提醒：模型并不走这棵树
  const off = m.treeNotice(overview(), false)
  assert.equal(off.tone, 'warn')
  assert.match(off.text, /平铺/)
  // 但空范围优先说「没有记忆」
  assert.match(m.treeNotice(overview({ reason: 'empty', ready: false }), false).text, /还没有记忆/)
})

test('模型摘要占比与覆盖文字', () => {
  assert.equal(m.summarizedPercent(overview({ summarized: 3, branches: 7 })), 43)
  assert.equal(m.summarizedPercent(overview({ branches: 0, summarized: 0 })), 0)
  assert.equal(m.summarizedPercent(null), 0)
  assert.equal(m.coverageText(overview()), '24 / 24')
  assert.equal(m.coverageText(overview({ ready: false, covered: 0 })), '0 / 24')
  assert.equal(m.coverageText(undefined), '—')
})

test('定位：节点链并入已加载的树并逐级展开，不改入参', () => {
  const root = { id: 'r', label: 'Memory themes', count: 3, level: 2, child_count: 1, children: [{ id: 'b', label: 'x', count: 3, level: 1, child_count: 2 }] }
  const branch = { id: 'b', label: 'x', count: 3, level: 1, child_count: 2, children: [{ id: 'l1', label: 'fact', count: 1, level: 0, record_id: 'm1', child_count: 0 }] }
  const leaf = { id: 'l1', label: 'fact', count: 1, level: 0, record_id: 'm1', child_count: 0 }
  const before = { loaded: {}, expanded: { keep: true } }
  const next = m.mergeChain(before, [root, branch, leaf])
  assert.deepEqual(Object.keys(next.loaded).sort(), ['b', 'l1', 'r'])
  assert.equal(next.expanded.r, true)
  assert.equal(next.expanded.b, true)
  assert.equal(next.expanded.l1, undefined)
  assert.equal(next.expanded.keep, true)
  assert.deepEqual(before, { loaded: {}, expanded: { keep: true } })
  // 已加载的子节点优先
  assert.equal(m.childrenOf(root.children[0], next.loaded)[0].id, 'l1')
  assert.deepEqual(m.childrenOf(leaf, next.loaded), [])
})

test('预算文字', () => {
  assert.equal(m.budgetText(612, 1000), '612 / 1000（61%）')
  assert.equal(m.budgetText(2000, 1000), '2000 / 1000（100%）')
  assert.equal(m.budgetText(5, 0), '5')
  assert.equal(m.budgetText(undefined, undefined), '0')
})
