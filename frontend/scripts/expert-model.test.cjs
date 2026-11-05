// 专家（对话角色）的纯展示逻辑单测：分组顺序、标签、已删除状态、切换后的提示语。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/stores/expertModel.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const m = mod.exports

const ex = (id, category, extra = {}) => ({
  id, name: `专家${id}`, summary: `${id} 的一句话`, category, source: 'official', order: 10, skills: [], starters: [], ...extra,
})

test('分类标签：已知的用中文，空的算通用，用户自己起的原样显示', () => {
  assert.equal(m.categoryLabel('office'), '办公')
  assert.equal(m.categoryLabel('agentgo'), 'AgentGo')
  assert.equal(m.categoryLabel(''), '通用')
  assert.equal(m.categoryLabel(undefined), '通用')
  assert.equal(m.categoryLabel('财务'), '财务')
})

test('分组：已知分类按固定顺序，自定义分类在后按字母序，组内顺序不动', () => {
  const groups = m.groupExperts([
    ex('w1', 'writing'), ex('z1', 'zeta'), ex('o2', 'office'), ex('c1', 'code'), ex('a1', 'alpha'), ex('o1', 'office'), ex('g1', ''),
  ])
  assert.deepEqual(groups.map(g => g.category), ['office', 'code', 'writing', 'general', 'alpha', 'zeta'])
  assert.deepEqual(groups[0].experts.map(e => e.id), ['o2', 'o1'])
  assert.equal(groups[0].label, '办公')
  assert.equal(groups.find(g => g.category === 'general').experts[0].id, 'g1')
})

test('分组：没有专家、缺 id 的条目都不会抛错也不出现', () => {
  assert.deepEqual(m.groupExperts(null), [])
  assert.deepEqual(m.groupExperts(undefined), [])
  assert.deepEqual(m.groupExperts([]), [])
  assert.deepEqual(m.groupExperts([{ id: ' ', category: 'office' }, null]), [])
})

test('来源标签：官方不标', () => {
  assert.equal(m.sourceLabel(ex('a', 'code')), '')
  assert.equal(m.sourceLabel(ex('a', 'code', { source: 'user' })), '我的')
  assert.equal(m.sourceLabel(ex('a', 'code', { source: 'workspace' })), '工作区')
  assert.equal(m.sourceLabel(null), '')
})

test('悬浮提示：介绍、来源与覆盖、缺少的技能各占一行', () => {
  const plain = ex('a', 'code')
  assert.equal(m.expertTooltip(plain), 'a 的一句话')
  const mine = ex('a', 'code', { source: 'user', overrides: 'official', skills: [{ id: 'x', available: true }, { id: 'y', available: false }] })
  assert.equal(m.expertTooltip(mine), 'a 的一句话\n我的专家，盖掉了同名的内置专家\n当前没有这些技能：y')
  assert.equal(m.expertTooltip(ex('a', 'code', { source: 'workspace' })), 'a 的一句话\n工作区专家')
  assert.deepEqual(m.missingSkills(mine), ['y'])
  assert.deepEqual(m.missingSkills(null), [])
})

test('专家状态：没绑定、绑定且还在、绑定但文件没了', () => {
  assert.deepEqual(m.expertState(null), { kind: 'none', id: '', name: '', expert: null })
  assert.equal(m.expertState({ expert_id: '', expert: null }).kind, 'none')
  const live = m.expertState({ expert_id: 'bug-hunter', expert: ex('bug-hunter', 'code', { name: '故障排查工程师' }), missing: false })
  assert.equal(live.kind, 'active')
  assert.equal(live.name, '故障排查工程师')
  const gone = m.expertState({ expert_id: 'mine', expert: null, missing: true })
  assert.deepEqual(gone, { kind: 'missing', id: 'mine', name: 'mine', expert: null })
  // 回答里 missing 为真时，即使带了 expert 也不当作还在
  assert.equal(m.expertState({ expert_id: 'mine', expert: ex('mine', 'code'), missing: true }).kind, 'missing')
})

test('标签：已删除的专家要说"已删除"，没有专家是空串', () => {
  assert.equal(m.expertChip(m.expertState(null)), '')
  assert.equal(m.expertChip(m.expertState({ expert_id: 'a', expert: ex('a', 'code', { name: '甲' }) })), '甲')
  assert.equal(m.expertChip(m.expertState({ expert_id: 'a', missing: true })), 'a（已删除）')
  assert.ok(m.MISSING_EXPERT_HINT.includes('普通对话'))
})

test('切换提示：成功点明生效时机，失败给原因，取消和切换分开说', () => {
  const set = { success: true, expert_id: 'a', expert: ex('a', 'code', { name: '甲' }) }
  assert.deepEqual(m.expertNotice(set, false), { tone: 'success', text: '已切换为「甲」' })
  assert.deepEqual(m.expertNotice(set, true), { tone: 'success', text: '已切换为「甲」；从下一条消息起生效' })
  assert.deepEqual(m.expertNotice({ success: true, expert_id: '' }, true), { tone: 'info', text: '已取消专家；从下一条消息起生效' })
  assert.deepEqual(m.expertNotice({ success: true, expert_id: '' }, false), { tone: 'info', text: '已取消专家' })
  assert.deepEqual(m.expertNotice({ success: false, error: '没有叫 "x" 的专家' }, true), { tone: 'error', text: '没有叫 "x" 的专家' })
  assert.deepEqual(m.expertNotice({ success: false }, false), { tone: 'error', text: '设置专家失败' })
  assert.deepEqual(m.expertNotice(null, false), { tone: 'error', text: '设置专家失败' })
})

test('示例提示：去空白、去空条目、最多 6 条', () => {
  assert.deepEqual(m.pickStarters(null), [])
  assert.deepEqual(m.pickStarters({ starters: [' 一 ', '', '  ', '二'] }), ['一', '二'])
  assert.equal(m.pickStarters({ starters: ['1', '2', '3', '4', '5', '6', '7', '8'] }).length, 6)
  assert.equal(m.pickStarters({ starters: ['1', '2', '3'] }, 2).length, 2)
})

test('目录说明：有哪个说哪个，都没有就是空串', () => {
  assert.equal(m.folderHint('C:\\d\\experts', 'C:\\w\\.agentgo\\experts'), '我的专家：C:\\d\\experts\n工作区专家：C:\\w\\.agentgo\\experts')
  assert.equal(m.folderHint('C:\\d\\experts', ''), '我的专家：C:\\d\\experts')
  assert.equal(m.folderHint('', undefined), '')
})
