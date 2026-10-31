// 「运行中」视图的纯展示逻辑单测：状态栏文字、行标题、耗时读法、徽标合并、变化判断。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/stores/runningWorkModel.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const m = mod.exports

const item = (over) => ({ kind: 'session', id: 'session:a', session_id: 'a', known: true, title: '写周报', state: 'running', stoppable: true, ...over })

test('状态栏按钮：在跑的、等你的、两者都有、什么都没有', () => {
  assert.equal(m.chipText({ running: 3, waiting: 0 }), '运行中 3')
  assert.equal(m.chipText({ running: 0, waiting: 2 }), '待处理 2')
  assert.equal(m.chipText({ running: 3, waiting: 1 }), '运行中 3 · 待处理 1')
  assert.equal(m.chipText({ running: 0, waiting: 0 }), '没有运行中的工作')
  assert.equal(m.chipTone({ running: 3, waiting: 1 }), 'attention')
  assert.equal(m.chipTone({ running: 3, waiting: 0 }), 'busy')
  assert.equal(m.chipTone({ running: 0, waiting: 0 }), 'idle')
  assert.equal(m.workTotal({ running: 3, waiting: 1 }), 4)
})

test('行标题：会话用自己的标题，打不开的会话、任务、引擎运行、等待各有说法', () => {
  assert.equal(m.itemTitle(item()), '写周报')
  assert.equal(m.itemTitle(item({ title: '   ' })), '未命名对话')
  assert.equal(m.itemTitle(item({ known: false, title: '', session_id: 'x' })), '后台会话')
  assert.equal(m.itemTitle(item({ known: false, title: '', session_id: 'app_matrix_coordinator' })), '团队协调（后台）')
  assert.equal(m.itemTitle(item({ kind: 'task', task_kind: 'cron', title: '每日巡检' })), '定时任务 · 每日巡检')
  assert.equal(m.itemTitle(item({ kind: 'task', task_kind: 'swarm', title: '' })), '多智能体协作')
  assert.equal(m.itemTitle(item({ kind: 'task', task_kind: 'something-new', title: '干活' })), '后台任务 · 干活')
  assert.equal(m.itemTitle(item({ kind: 'run', engine: 'voice' })), '后台运行 · voice')
  assert.equal(m.itemTitle(item({ kind: 'run', engine: '' })), '后台运行')
  assert.equal(m.itemTitle(item({ kind: 'waiting', source: 'workflow' })), '工作流在等你')
  assert.equal(m.itemTitle(item({ kind: 'waiting', source: '' })), '后台流程在等你')
})

test('耗时：秒、分秒、整分钟、小时、天', () => {
  assert.equal(m.durationText(0), '0 秒')
  assert.equal(m.durationText(59), '59 秒')
  assert.equal(m.durationText(60), '1 分钟')
  assert.equal(m.durationText(185), '3 分 5 秒')
  assert.equal(m.durationText(3600), '1 小时')
  assert.equal(m.durationText(3600 + 600 + 5), '1 小时 10 分')
  assert.equal(m.durationText(86400 * 3 + 5), '3 天')
  assert.equal(m.durationText(-5), '0 秒')
  assert.equal(m.durationText(NaN), '0 秒')
})

test('标题下的小字：状态 + 工具 + 项数 + 所在对话 + 耗时', () => {
  assert.equal(m.itemDetail(item({ since: 1000 }), 1185), '运行中 · 已运行 3 分 5 秒')
  assert.equal(m.itemDetail(item({ since: 1000 }), 1001), '运行中 · 刚开始')
  assert.equal(m.itemDetail(item({ since: 0 }), 5000), '运行中', '开始时间不明就不编造耗时')
  assert.equal(m.itemDetail(item({ since: 1000 }), 0), '运行中', '没有服务端时间也不编造')
  assert.equal(m.itemDetail(item({ state: 'queued', since: 100 }), 130), '排队中 · 已排队 30 秒')
  assert.equal(
    m.itemDetail(item({ state: 'awaiting_approval', tool: 'execute_bash', count: 1, since: 100 }), 140),
    '等你批准 · execute_bash · 已等待 40 秒',
  )
  assert.equal(
    m.itemDetail(item({ state: 'awaiting_approval', tool: 'write_file', count: 3, since: 100 }), 160),
    '等你批准 · write_file · 3 项 · 已等待 1 分钟',
  )
  assert.equal(m.itemDetail(item({ state: 'awaiting_answer', tool: 'ask_user', count: 1 }), 160), '等你回答', '回答不用说工具名')
  assert.equal(
    m.itemDetail(item({ kind: 'task', task_kind: 'cron', session_title: '周报', since: 100 }), 110),
    '运行中 · 在「周报」里 · 已运行 10 秒',
  )
})

test('按钮：只有打得开的对话能跳；等你的叫「去处理」；任务叫「取消」', () => {
  assert.equal(m.canJump(item()), true)
  assert.equal(m.canJump(item({ known: false })), false, '已删除或内部的对话打不开')
  assert.equal(m.canJump(item({ session_id: '' })), false)
  assert.equal(m.canJump(item({ session_id: undefined })), false)
  assert.equal(m.jumpLabel(item({ state: 'awaiting_approval' })), '去处理')
  assert.equal(m.jumpLabel(item({ state: 'awaiting_answer' })), '去处理')
  assert.equal(m.jumpLabel(item()), '查看')
  assert.equal(m.stopLabel(item({ kind: 'task' })), '取消')
  assert.equal(m.stopLabel(item()), '停止')
  assert.equal(m.isWaiting(item({ state: 'awaiting_answer' })), true)
  assert.equal(m.isWaiting(item({ state: 'queued' })), false)
})

test('会话列表的圆点：在跑、等待；排队的、没有对话的不画', () => {
  const badges = m.remoteBadges({
    items: [
      item({ session_id: 'run1', state: 'running' }),
      item({ session_id: 'ask1', state: 'awaiting_approval' }),
      item({ session_id: 'ask2', state: 'awaiting_answer' }),
      item({ kind: 'task', session_id: 'job1', state: 'running' }),
      item({ kind: 'task', session_id: 'job2', state: 'queued' }),
      item({ kind: 'task', session_id: '', state: 'running' }),
      item({ kind: 'run', session_id: undefined, state: 'running' }),
    ],
  })
  assert.deepEqual(badges, { run1: 'running', ask1: 'awaiting', ask2: 'awaiting', job1: 'running' })
  assert.deepEqual(m.remoteBadges({ items: [] }), {})
})

test('本页自己的运行状态优先，后端的补上其余', () => {
  const merged = m.mergeBadges({ a: 'awaiting', b: 'running' }, { a: 'running', c: 'running' })
  assert.deepEqual(merged, { a: 'awaiting', b: 'running', c: 'running' })
  assert.deepEqual(m.mergeBadges({}, { c: 'awaiting' }), { c: 'awaiting' })
  assert.deepEqual(m.mergeBadges({ a: 'running' }, {}), { a: 'running' })
})

test('两次轮询是否一样：不看服务端时间，内容变了才算变', () => {
  const a = { items: [item()], running: 1, waiting: 0, at: 100 }
  assert.equal(m.sameWork(a, { items: [item()], running: 1, waiting: 0, at: 105 }), true)
  assert.equal(m.sameWork(a, { items: [item({ state: 'awaiting_approval' })], running: 1, waiting: 0, at: 100 }), false)
  assert.equal(m.sameWork(a, { items: [item({ since: 7 })], running: 1, waiting: 0, at: 100 }), false)
  assert.equal(m.sameWork(a, { items: [], running: 0, waiting: 0, at: 100 }), false)
  assert.equal(m.sameWork(a, { items: [item()], running: 1, waiting: 1, at: 100 }), false)
  assert.equal(m.sameWork(m.emptyWork(), m.emptyWork()), true)
})

test('任务：种类与状态的叫法、颜色、被打断的判断', () => {
  assert.equal(m.taskKindLabel('cron'), '定时任务')
  assert.equal(m.taskKindLabel('trigger'), '事件唤醒')
  assert.equal(m.taskKindLabel('matrix_followup'), '团队后续处理')
  assert.equal(m.taskKindLabel(undefined), '后台任务')
  assert.equal(m.taskStatusText('queued'), '排队中')
  assert.equal(m.taskStatusText('completed'), '已完成')
  assert.equal(m.taskStatusText('failed'), '失败')
  assert.equal(m.taskStatusText('cancelled'), '已取消')
  assert.equal(m.taskStatusText('weird'), 'weird')
  assert.equal(m.taskStatusText(undefined), '未知')
  assert.equal(m.taskStatusTone('running'), 'running')
  assert.equal(m.taskStatusTone('queued'), 'running')
  assert.equal(m.taskStatusTone('failed'), 'error')
  assert.equal(m.taskStatusTone('completed'), 'done')
  assert.equal(m.taskStatusTone('cancelled'), 'done')

  assert.equal(m.taskInputSummary({ kind: 'manual', input: '\n\n  整理本周日志  \n其余' }), '整理本周日志')
  assert.equal(m.taskInputSummary({ kind: 'manual', input: '长'.repeat(80) }), '长'.repeat(60) + '…', '按字数截，不按字节截')
  assert.equal(m.taskInputSummary({ kind: 'manual', input: '😀'.repeat(61) }), '😀'.repeat(60) + '…', '不把一个表情劈成两半')
  assert.equal(m.taskInputSummary({ kind: 'swarm', input: '{"topic":"方案评审","agent_ids_json":"[]"}' }), '方案评审')
  assert.equal(m.taskInputSummary({ kind: 'swarm', input: 'not json' }), 'not json')
  assert.equal(m.taskInputSummary({ kind: 'swarm', input: '{"topic":"  "}' }), '{"topic":"  "}', '主题是空的就按原文')
  assert.equal(m.taskInputSummary({ kind: 'manual' }), '')

  const live = new Set(['t1'])
  assert.equal(m.isInterrupted('running', live, 't1'), false, '本程序还在跑的不算打断')
  assert.equal(m.isInterrupted('running', live, 't2'), true, '库里写着 running 但本程序没在跑：上次被打断')
  assert.equal(m.isInterrupted('queued', live, 't2'), true)
  assert.equal(m.isInterrupted('completed', live, 't2'), false)
})

test('弹层挪位：窄窗口里不伸出窗外，宽窗口里不动', () => {
  // 实测：窗口 720 宽时弹层 x=375、宽 400，右边到 775，伸出 55 像素，右侧「停止」按钮被切掉。
  assert.equal(m.panelShift(375, 775, 720), -63, '右边出界：往左挪到离窗口右边 8 像素')
  assert.equal(m.panelShift(375, 775, 1920), 0, '宽窗口：不用动')
  assert.equal(m.panelShift(375, 775, 783), 0, '刚好留够 8 像素边：不用动')
  assert.equal(m.panelShift(375, 775, 782), -1, '少 1 像素就挪 1 像素')
  assert.equal(m.panelShift(-20, 380, 720), 28, '左边出界：往右挪回来')
  assert.equal(m.panelShift(10, 700, 600), -2, '窗口比弹层还窄：左边优先对齐，右边接受被截')
  assert.equal(m.panelShift(375, 775, 720, 0), -55, '边距可调')
  assert.equal(m.panelShift(375.4, 775.4, 720), -63, '结果是整数像素')
})
