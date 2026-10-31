// 按轮回退的纯展示逻辑单测：回退点怎么挂到消息上、什么时候出按钮、确认框与结果怎么说。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/stores/turnCheckpointModel.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const m = mod.exports

const user = (content, meta) => ({ role: 'user', type: 'text', content, meta })
const assistant = (content, meta) => ({ role: 'assistant', type: 'text', content, meta })

test('会话行 → meta：回退点只认用户消息，说明行只认约定的 kind，别的键不放进来', () => {
  assert.equal(m.rowMeta({ role: 'user', content: 'hi' }), undefined)
  assert.deepEqual(m.rowMeta({ role: 'user', checkpoint: ' cp_1 ' }), { checkpoint: 'cp_1' })
  assert.equal(m.rowMeta({ role: 'assistant', checkpoint: 'cp_1' }), undefined)
  assert.deepEqual(m.rowMeta({ role: 'assistant', kind: 'workspace_rollback', rollback_target: 'cp_1' }), { kind: 'workspace_rollback' })
  assert.equal(m.rowMeta({ role: 'assistant', kind: 'something_else' }), undefined)
  assert.deepEqual(m.rowMeta({ role: 'user', images: ['a.png'], checkpoint: 'cp_2' }), { images: ['a.png'], checkpoint: 'cp_2' })
  assert.equal(m.rowMeta(null), undefined)
})

test('checkpointOf / isRollbackNote 只对该对的角色成立', () => {
  assert.equal(m.checkpointOf(user('x', { checkpoint: 'cp_1' })), 'cp_1')
  assert.equal(m.checkpointOf(assistant('x', { checkpoint: 'cp_1' })), '')
  assert.equal(m.checkpointOf(null), '')
  assert.equal(m.isRollbackNote(assistant('x', { kind: 'workspace_rollback' })), true)
  assert.equal(m.isRollbackNote(user('x', { kind: 'workspace_rollback' })), false)
  assert.equal(m.isRollbackNote(assistant('x')), false)
})

test('说明行只画第一句；提示词里带句号也不被截断', () => {
  const full = '【系统记录】用户把工作区文件回退到了「修一下。再测一遍」这一轮开始之前的状态（还原 2 个、删除 1 个文件）。此后对话中提到的文件改动可能已经不存在。'
  assert.equal(m.noteHeadline(full), '用户把工作区文件回退到了「修一下。再测一遍」这一轮开始之前的状态（还原 2 个、删除 1 个文件）')
  assert.equal(m.noteHeadline('只有一句。后面还有'), '只有一句')
  assert.equal(m.noteHeadline(undefined), '')
})

test('started 事件把回退点挂到这一轮的用户消息上（segStart 指向它，后面跟着流式占位）', () => {
  const msgs = [user('旧的', { checkpoint: 'cp_0' }), assistant('回答'), user('新的'), assistant('', { streaming: true })]
  assert.equal(m.tagStartedCheckpoint(msgs, 2, { checkpoint_id: 'cp_1' }, true), 'tagged')
  assert.deepEqual(msgs[2].meta, { checkpoint: 'cp_1' })
  assert.deepEqual(msgs[0].meta, { checkpoint: 'cp_0' }, '旧消息不动')
  assert.deepEqual(msgs[3].meta, { streaming: true }, '占位消息不动')
})

test('started 事件：保留消息上已有的 meta（比如图片）', () => {
  const msgs = [user('看图', { images: ['a.png'] })]
  m.tagStartedCheckpoint(msgs, 0, { checkpoint_id: 'cp_1' }, true)
  assert.deepEqual(msgs[0].meta, { images: ['a.png'], checkpoint: 'cp_1' })
})

test('started 事件：不是这一页发起的运行（IM、自动化）绝不打标——segStart 是过期的，会挂错消息', () => {
  const msgs = [user('历史里的第一条'), assistant('答')]
  assert.equal(m.tagStartedCheckpoint(msgs, 0, { checkpoint_id: 'cp_x' }, false), 'none')
  assert.equal(msgs[0].meta, undefined)
  assert.equal(m.tagStartedCheckpoint(msgs, 0, { checkpoint_error: 'boom' }, false), 'none', '也不为别处的运行弹建点失败的警告')
})

test('started 事件：segStart 不指向用户文字消息就不打标，也不往回找别的消息', () => {
  const msgs = [user('问'), assistant('答'), { role: 'assistant', type: 'tool_call', content: '' }]
  assert.equal(m.tagStartedCheckpoint(msgs, 2, { checkpoint_id: 'cp_9' }, true), 'none')
  assert.equal(m.tagStartedCheckpoint(msgs, 1, { checkpoint_id: 'cp_9' }, true), 'none')
  assert.equal(m.tagStartedCheckpoint(msgs, 7, { checkpoint_id: 'cp_9' }, true), 'none')
  assert.equal(m.tagStartedCheckpoint([{ role: 'user', type: 'question', content: '?' }], 0, { checkpoint_id: 'cp_9' }, true), 'none')
  assert.equal(msgs[0].meta, undefined, '最近的用户消息也没被挂上')
})

test('started 事件：已经有回退点的消息不被覆盖', () => {
  const msgs = [user('x', { checkpoint: 'cp_old' })]
  assert.equal(m.tagStartedCheckpoint(msgs, 0, { checkpoint_id: 'cp_new' }, true), 'none')
  assert.deepEqual(msgs[0].meta, { checkpoint: 'cp_old' })
})

test('started 事件：没带回退点是 none；建点失败是 failed 且不写任何编号', () => {
  assert.equal(m.tagStartedCheckpoint([user('x')], 0, {}, true), 'none')
  assert.equal(m.tagStartedCheckpoint([user('x')], 0, undefined, true), 'none')
  const msgs = [user('x')]
  assert.equal(m.tagStartedCheckpoint(msgs, 0, { checkpoint_error: 'checkpoints are paused' }, true), 'failed')
  assert.equal(msgs[0].meta, undefined)
})

test('建点失败的提示：认得出的原因翻成人话，认不出的原样给，并强调对话不受影响', () => {
  assert.match(m.checkpointWarning('checkpoints are paused for this workspace: too many files'), /暂停约 10 分钟/)
  assert.match(m.checkpointWarning('too many files'), /文件太多/)
  assert.match(m.checkpointWarning('context deadline exceeded'), /超时/)
  assert.match(m.checkpointWarning('weird'), /weird/)
  assert.match(m.checkpointWarning(''), /未知原因/)
  assert.match(m.checkpointWarning('x'), /对话本身不受影响/)
})

test('按钮只在：有回退点、回退点还在、没在运行 时出现', () => {
  const live = m.liveCheckpointIds({ checkpoints: [{ id: 'cp_1' }, { id: ' cp_2 ' }, { id: '' }, {}] })
  assert.deepEqual(Object.keys(live).sort(), ['cp_1', 'cp_2'])
  const msg = user('x', { checkpoint: 'cp_1' })
  assert.equal(m.canRollback(msg, live, false), true)
  assert.equal(m.canRollback(msg, live, true), false, '运行中不给回退')
  assert.equal(m.canRollback(user('x', { checkpoint: 'cp_gone' }), live, false), false, '回退点已被挤掉或重启后失效')
  assert.equal(m.canRollback(user('x'), live, false), false, '没建过点的消息')
  assert.equal(m.canRollback(assistant('x', { checkpoint: 'cp_1' }), live, false), false)
  assert.deepEqual(m.liveCheckpointIds(null), {})
})

const change = (action, p, extra = {}) => ({ path: p, name: p.split('/').pop(), action, before_size: 1, after_size: 2, skip: false, ...extra })
const preview = (changes, extra = {}) => {
  const count = a => changes.filter(c => !c.skip && c.action === a).length
  return {
    checkpoint: { id: 'cp_1', kind: 'turn', seq: 1, prompt: '加个登录页', created_at: 1, files: 10, skipped: 0, root: '/w' },
    changes,
    restore: count('restore'), recreate: count('recreate'), delete: count('delete'),
    skipped: changes.filter(c => c.skip).length, unverified: 0,
    ...extra,
  }
}

test('确认框：说清楚恢复/找回/删除各多少，列出文件，并提醒手动修改也会被覆盖、可以撤销', () => {
  const d = m.describeRollback(preview([change('restore', 'src/a.ts'), change('recreate', 'docs/b.md'), change('delete', 'tmp/c.txt')]))
  assert.equal(d.empty, false)
  assert.match(d.text, /回到「加个登录页」开始之前/)
  assert.match(d.text, /恢复 1 个、找回 1 个、删除 1 个（共 3 个文件）/)
  assert.match(d.text, /恢复 {2}src\/a\.ts/)
  assert.match(d.text, /找回 {2}docs\/b\.md/)
  assert.match(d.text, /删除 {2}tmp\/c\.txt/)
  assert.match(d.text, /手动改过的文件也会一起恢复原样/)
  assert.match(d.text, /撤销这次回退/)
  assert.match(d.text, /超过 8 MB/)
})

test('确认框：只列前 10 个，其余用数字交代；零计数的动作不出现', () => {
  const many = Array.from({ length: 13 }, (_, i) => change('restore', `f${i}.txt`))
  const d = m.describeRollback(preview(many))
  assert.match(d.text, /恢复 13 个（共 13 个文件）/)
  assert.doesNotMatch(d.text, /找回|删除 \d+ 个/)
  assert.equal(d.text.split('\n').filter(l => /^ {2}恢复 {2}f\d+\.txt$/.test(l)).length, 10)
  assert.match(d.text, /…… 还有 3 个/)
})

test('确认框：后端只带了部分 changes 时，仍按完整计数交代没列出来的', () => {
  const shown = Array.from({ length: 200 }, (_, i) => change('restore', `f${i}.txt`))
  const d = m.describeRollback(preview(shown, { restore: 450 }))
  assert.match(d.text, /恢复 450 个（共 450 个文件）/)
  assert.match(d.text, /…… 还有 440 个/)
})

test('确认框：会放过的文件单独列出并说明原因，不混进"将要改"的清单', () => {
  const d = m.describeRollback(preview([
    change('restore', 'src/a.ts'),
    change('restore', 'big.bin', { skip: true, reason: 'original content was not kept (over the size limit)' }),
    change('restore', 'huge.log', { skip: true, reason: 'current content is too large to keep a safety copy' }),
  ]))
  assert.match(d.text, /恢复 1 个（共 1 个文件）/)
  assert.match(d.text, /有 2 个文件不会动/)
  assert.match(d.text, /big\.bin：体积过大或超出内存预算/)
  assert.match(d.text, /huge\.log：现有内容太大，没法先备份/)
  assert.doesNotMatch(d.text, /^ {2}恢复 {2}big\.bin/m)
})

test('确认框：没有任何可回退的内容时 empty=true，并说明是"已经一致"还是"都被放过"', () => {
  const same = m.describeRollback(preview([]))
  assert.equal(same.empty, true)
  assert.match(same.text, /已经一致|一致/)
  const blocked = m.describeRollback(preview([change('restore', 'big.bin', { skip: true, reason: 'original content was not kept' })]))
  assert.equal(blocked.empty, true)
  assert.match(blocked.text, /没有可以回退的文件/)
  assert.match(blocked.text, /big\.bin/)
  assert.equal(m.describeRollback(null).empty, true)
})

test('确认框：没有提示词时不留空引号', () => {
  const p = preview([change('restore', 'a.txt')])
  p.checkpoint.prompt = '  '
  assert.match(m.describeRollback(p).text, /^回到这一轮开始之前/)
})

test('后端的原因 → 中文：认得的翻译，认不得的原样给', () => {
  assert.match(m.skipReasonText('original content was not kept'), /没有保存原内容/)
  assert.match(m.skipReasonText('current content is too large to keep a safety copy'), /撤销不了/)
  assert.match(m.skipReasonText('file could not be read when the checkpoint was taken'), /无法读取/)
  assert.equal(m.skipReasonText('something odd'), 'something odd')
  assert.equal(m.skipReasonText(''), '无法处理')
})

test('失败提示：忙、失效、换过工作区都是警告而不是错误；其余才是错误，且把原因带上', () => {
  assert.deepEqual(m.rollbackError({ busy: true, error: '1 run(s) in progress; stop them first' }), { tone: 'warning', text: '现在不能回退：有任务正在运行，请先停止或等它结束' })
  assert.match(m.rollbackError({ busy: '2 request(s) waiting for approval; resolve them first' }).text, /等你批准/)
  assert.equal(m.rollbackError({ error: 'checkpoint not found: x' }).tone, 'warning')
  assert.match(m.rollbackError({ error: 'checkpoint not found: x' }).text, /已经失效/)
  assert.match(m.rollbackError({ error: 'checkpoint belongs to another workspace' }).text, /工作区已经切换/)
  assert.deepEqual(m.rollbackError({ error: 'disk full' }), { tone: 'error', text: '回退失败：disk full' })
  assert.equal(m.rollbackError(null).tone, 'error')
})

const result = (extra = {}) => ({ restored: 0, recreated: 0, deleted: 0, skipped: [], failed: [], unverified: 0, undo_id: 'cp_u', ...extra })

test('回退结果：成功是 success，并交代做了什么；有放过的文件要提', () => {
  const ok = m.rollbackNotice({ success: true, result: result({ restored: 2, deleted: 1 }) })
  assert.equal(ok.tone, 'success')
  assert.match(ok.text, /恢复 2 个、删除 1 个/)
  const partial = m.rollbackNotice({ success: true, result: result({ restored: 1, skipped: [{ path: 'big.bin', reason: 'x' }] }) })
  assert.equal(partial.tone, 'success')
  assert.match(partial.text, /另有 1 个文件无法处理，保持原样/)
})

test('回退结果：有写失败的文件就是警告，点名第一个，并指出可以撤销——不能报"成功"', () => {
  const n = m.rollbackNotice({ success: true, result: result({ restored: 1, failed: [{ path: 'locked.txt', reason: 'Access is denied' }, { path: 'b', reason: 'x' }] }) })
  assert.equal(n.tone, 'warning')
  assert.match(n.text, /有 2 个没能写回（locked\.txt：Access is denied）/)
  assert.match(n.text, /撤销这次回退/)
})

test('回退结果：什么都没动是 info，且分得清"本来就一致"和"都被放过"', () => {
  assert.match(m.rollbackNotice({ success: true, result: result() }).text, /已经和这一轮开始之前一致/)
  const blocked = m.rollbackNotice({ success: true, result: result({ skipped: [{ path: 'a', reason: 'x' }] }) })
  assert.equal(blocked.tone, 'info')
  assert.match(blocked.text, /没有文件被回退/)
})

test('撤销回退的提示：措辞是"撤销"，不再劝人去撤销', () => {
  assert.match(m.rollbackNotice({ success: true, result: result({ restored: 2 }) }, true).text, /^已撤销上一次回退：恢复 2 个/)
  const failed = m.rollbackNotice({ success: false, result: result({ restored: 1, failed: [{ path: 'a', reason: 'locked' }] }) }, true)
  assert.equal(failed.tone, 'warning')
  assert.doesNotMatch(failed.text, /点「撤销这次回退」/)
  assert.match(m.rollbackNotice({ success: true, result: result() }, true).text, /已经是回退之前的样子/)
  assert.match(m.rollbackNotice({ success: true, result: result({ skipped: [{ path: 'a', reason: 'x' }] }) }, true).text, /没有文件被撤销/)
})

test('撤销条：撤销执行过或回退点没了就收起；"现在忙"之类的拒绝留着让人再点', () => {
  assert.equal(m.undoSettled({ success: true, result: result({ restored: 1 }) }), true)
  assert.equal(m.undoSettled({ success: false, result: result({ failed: [{ path: 'a', reason: 'x' }] }) }), true)
  assert.equal(m.undoSettled({ success: false, error: 'checkpoint not found: cp_u' }), true)
  assert.equal(m.undoSettled({ success: false, error: 'checkpoint belongs to another workspace' }), true)
  assert.equal(m.undoSettled({ success: false, busy: true, error: '1 run(s) in progress; stop them first' }), false)
  assert.equal(m.undoSettled({ success: false, error: 'disk full' }), false)
  assert.equal(m.undoSettled(null), false)
})

test('回退结果：没有 result 就按失败处理（后端拒绝了），不会当成成功', () => {
  assert.equal(m.rollbackNotice({ success: false, error: 'disk full' }).tone, 'error')
  assert.equal(m.rollbackNotice({ success: false, busy: true, error: '1 run(s) in progress' }).tone, 'warning')
  assert.equal(m.rollbackNotice(null).tone, 'error')
})

test('一句话摘要：只写做了的动作', () => {
  assert.equal(m.rollbackSummary(result({ restored: 3 })), '恢复 3 个')
  assert.equal(m.rollbackSummary(result({ recreated: 1, deleted: 2 })), '找回 1 个、删除 2 个')
  assert.equal(m.rollbackSummary(null), '')
})
