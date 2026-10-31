// 会话分叉的纯展示逻辑单测：来处识别、来处文字、分叉结果的提示语气。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/stores/forkModel.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const m = mod.exports

test('只有带 parent_id 的会话才是分叉，缺字段不抛错', () => {
  assert.equal(m.forkLineage(null), null)
  assert.equal(m.forkLineage(undefined), null)
  assert.equal(m.forkLineage({}), null)
  assert.equal(m.forkLineage({ parent_id: '   ' }), null)
  assert.deepEqual(m.forkLineage({ parent_id: 'sess_a', parent_title: ' 重构方案 ' }), { parentId: 'sess_a', parentTitle: '重构方案', gone: false })
  assert.deepEqual(m.forkLineage({ parent_id: 'sess_a', parent_gone: true }), { parentId: 'sess_a', parentTitle: '未命名对话', gone: true })
})

test('来处文字：原对话还在与已删除各一句，长标题截断', () => {
  assert.equal(m.lineageText({ parentId: 'a', parentTitle: '重构方案', gone: false }), '分叉自「重构方案」')
  assert.equal(m.lineageText({ parentId: 'a', parentTitle: '重构方案', gone: true }), '分叉自「重构方案」（原对话已删除）')
  const long = m.lineageText({ parentId: 'a', parentTitle: '长'.repeat(40), gone: false })
  assert.equal(long, `分叉自「${'长'.repeat(24)}…」`)
  // 按字而不是按 UTF-16 单元截：emoji 不会被劈成半个
  const emoji = m.lineageText({ parentId: 'a', parentTitle: '😀'.repeat(30), gone: false })
  assert.ok(!emoji.includes('\ufffd') && emoji.includes('😀'.repeat(24) + '…'))
})

test('顶栏标签是短版：标题截到 12 字，已删除只留"（已删除）"，不会把关键信息挤出去', () => {
  assert.equal(m.lineageChip({ parentId: 'a', parentTitle: '重构方案', gone: false }), '分叉自「重构方案」')
  assert.equal(m.lineageChip({ parentId: 'a', parentTitle: '重构方案', gone: true }), '分叉自「重构方案」（已删除）')
  assert.equal(m.lineageChip({ parentId: 'a', parentTitle: '长'.repeat(30), gone: true }), `分叉自「${'长'.repeat(12)}…」（已删除）`)
})

test('分叉成功：说出来处与"互不影响但共用工作区"，警告并在后面', () => {
  const ok = m.forkNotice({ success: true, parent_title: '重构方案', warnings: [] })
  assert.equal(ok.tone, 'success')
  assert.equal(ok.text, '已从「重构方案」分叉；两边互不影响，但仍共用同一个工作区')
  const warned = m.forkNotice({ success: true, parent_title: '重构方案', warnings: ['对话已分叉，但计划任务清单没能一并复制：磁盘已满', ' '] })
  assert.equal(warned.tone, 'warning')
  assert.ok(warned.text.endsWith('。对话已分叉，但计划任务清单没能一并复制：磁盘已满'))
  assert.equal(m.forkNotice({ success: true }, '回退标题').text.includes('「回退标题」'), true)
  assert.equal(m.forkNotice({ success: true }).text.includes('「原对话」'), true)
})

test('分叉失败：运行中/待审批是提示不是错误，其余才是错误', () => {
  assert.deepEqual(m.forkNotice({ success: false, busy: true, error: '这条对话正在运行。等它结束，或先点停止，再分叉。' }),
    { tone: 'info', text: '这条对话正在运行。等它结束，或先点停止，再分叉。' })
  assert.deepEqual(m.forkNotice({ success: false, error: '这条对话已经不存在' }), { tone: 'error', text: '这条对话已经不存在' })
  assert.deepEqual(m.forkNotice({ success: false }), { tone: 'error', text: '分叉失败' })
  assert.deepEqual(m.forkNotice(null), { tone: 'error', text: '分叉失败' })
})

test('能不能分叉：按"最该先处理的原因"依次判断，能分叉返回空串', () => {
  const ok = { sessionId: 's1', sending: false, awaiting: false, forking: false, hasUserMessage: true }
  assert.equal(m.forkBlocker(ok), '')
  assert.equal(m.forkBlocker({ ...ok, forking: true }), '正在分叉，请稍候')
  assert.equal(m.forkBlocker({ ...ok, sessionId: '' }), '还没有对话，发一条消息后再分叉')
  assert.equal(m.forkBlocker({ ...ok, sending: true }), '这条对话正在运行。等它结束，或先点停止，再分叉。')
  assert.equal(m.forkBlocker({ ...ok, awaiting: true }), '这条对话停在等你批准或回答。先处理完它，再分叉。')
  assert.equal(m.forkBlocker({ ...ok, hasUserMessage: false }), '这条对话还是空的，没什么可分叉的')
  // 运行中同时在等批准：先说运行（用户要先停它），和后端的优先级一致
  assert.equal(m.forkBlocker({ ...ok, sending: true, awaiting: true }), '这条对话正在运行。等它结束，或先点停止，再分叉。')
})

test('新对话 id：优先取 session.id，其次 id，都没有是空串', () => {
  assert.equal(m.forkedId({ session: { id: 'sess_x' }, id: 'sess_y' }), 'sess_x')
  assert.equal(m.forkedId({ id: ' sess_y ' }), 'sess_y')
  assert.equal(m.forkedId({}), '')
  assert.equal(m.forkedId(null), '')
})
