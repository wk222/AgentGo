// 「用户钩子」卡片的纯展示逻辑单测：状态一句话、分组、结果与时间的读法、示例文件必须是合法 JSON。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/features/settings/hooksModel.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const m = mod.exports

const status = (over = {}) => ({
  enabled: true, path: 'C:\\data\\hooks.json', exists: true, counts: {}, total: 0, hooks: [], problems: [], recent: [], ...over,
})
const hook = (event, matcher, command = 'python x.py', on_failure = 'continue') => ({ event, matcher, command, timeout_s: 30, on_failure })

test('事件按对话里发生的先后排，名称与后端一致', () => {
  assert.deepEqual(m.HOOK_EVENTS.map(e => e.id), ['UserPromptSubmit', 'PreToolUse', 'PostToolUse', 'Stop'])
  assert.equal(m.eventLabel('PreToolUse'), '工具调用前')
  assert.equal(m.eventLabel('Stop'), '一轮结束后')
  assert.equal(m.eventLabel('SomethingNew'), 'SomethingNew')
})

test('运行结果：每种都有中文名和色调，未知结果不抛错', () => {
  assert.equal(m.resultLabel('ok'), '放行')
  assert.equal(m.resultLabel('blocked'), '已拦截')
  assert.equal(m.resultLabel('failed'), '脚本出错')
  assert.equal(m.resultLabel('timeout'), '超时')
  assert.equal(m.resultLabel('canceled'), '已取消')
  assert.equal(m.resultLabel('odd'), 'odd')
  assert.equal(m.resultTone('ok'), 'ok')
  assert.equal(m.resultTone('blocked'), 'warn')
  assert.equal(m.resultTone('failed'), 'bad')
  assert.equal(m.resultTone('timeout'), 'bad')
  assert.equal(m.resultTone('odd'), 'dim')
})

test('状态一句话：关闭、读不懂、还没配置、没有钩子、正常、有写法问题', () => {
  assert.deepEqual(m.hooksSummary(status({ enabled: false, disabled_reason: '已被 AGENTGO_USER_HOOKS=0 关闭' })), { tone: 'off', text: '已被 AGENTGO_USER_HOOKS=0 关闭' })
  assert.equal(m.hooksSummary(status({ enabled: false })).text, '用户钩子已关闭')

  const broken = m.hooksSummary(status({ error: 'invalid character', total: 2 }))
  assert.equal(broken.tone, 'bad')
  assert.match(broken.text, /上一份有效配置（2 个钩子）/)
  assert.match(m.hooksSummary(status({ error: 'invalid character', total: 0 })).text, /没有钩子生效/)

  assert.deepEqual(m.hooksSummary(status({ exists: false })), { tone: 'dim', text: '还没有配置：数据目录里没有 hooks.json' })
  assert.equal(m.hooksSummary(status()).text, '配置文件里没有钩子')
  assert.equal(m.hooksSummary(status({ problems: [{ where: 'a', message: 'b' }] })).text, '配置文件里没有一个钩子可用')

  assert.deepEqual(m.hooksSummary(status({ total: 3 })), { tone: 'ok', text: '已启用 3 个钩子' })
  const warn = m.hooksSummary(status({ total: 3, problems: [{ where: 'a', message: 'b' }, { where: 'c', message: 'd' }] }))
  assert.equal(warn.tone, 'warn')
  assert.equal(warn.text, '已启用 3 个钩子，另有 2 处写法有问题（已跳过）')
})

test('按事件分组：没配的事件也保留，同一事件内保持文件里的顺序', () => {
  const groups = m.groupHooks([hook('Stop', ''), hook('PreToolUse', 'Bash', 'a'), hook('PreToolUse', 'Edit', 'b')])
  assert.deepEqual(groups.map(g => g.id), ['UserPromptSubmit', 'PreToolUse', 'PostToolUse', 'Stop'])
  assert.deepEqual(groups.map(g => g.hooks.length), [0, 2, 0, 1])
  assert.deepEqual(groups[1].hooks.map(h => h.command), ['a', 'b'])
  assert.ok(groups.every(g => g.label && g.hint))
})

test('匹配范围：没写或 * 是所有工具；与工具无关的事件不显示匹配', () => {
  assert.equal(m.matcherLabel('PreToolUse', ''), '所有工具')
  assert.equal(m.matcherLabel('PreToolUse', undefined), '所有工具')
  assert.equal(m.matcherLabel('PostToolUse', ' * '), '所有工具')
  assert.equal(m.matcherLabel('PreToolUse', 'Bash|Edit'), 'Bash|Edit')
  assert.equal(m.matcherLabel('Stop', 'Bash'), '')
  assert.equal(m.matcherLabel('UserPromptSubmit', ''), '')
})

test('出错时怎么办', () => {
  assert.equal(m.onFailureLabel('block'), '出错时拦截')
  assert.equal(m.onFailureLabel('continue'), '出错时放行')
  assert.equal(m.onFailureLabel(''), '出错时放行')
})

test('时间：今天只写时分秒，其它日子带月/日，无效或为空不抛错', () => {
  const now = new Date(2026, 9, 28, 15, 0, 0)
  assert.equal(m.formatClock(new Date(2026, 9, 28, 9, 5, 7).toISOString(), now), '09:05:07')
  assert.equal(m.formatClock(new Date(2026, 9, 27, 23, 59, 1).toISOString(), now), '10/27 23:59:01')
  assert.equal(m.formatClock(undefined, now), '—')
  assert.equal(m.formatClock('', now), '—')
  assert.equal(m.formatClock('not a time', now), 'not a time')
})

test('耗时：毫秒、一位小数的秒、整数秒', () => {
  assert.equal(m.formatDuration(0), '0 毫秒')
  assert.equal(m.formatDuration(96), '96 毫秒')
  assert.equal(m.formatDuration(1240), '1.2 秒')
  assert.equal(m.formatDuration(2000), '2 秒')
  assert.equal(m.formatDuration(30000), '30 秒')
  assert.equal(m.formatDuration(-1), '—')
  assert.equal(m.formatDuration(NaN), '—')
})

test('校验和只显示前 8 位', () => {
  assert.equal(m.shortHash('7ce88e75430aa985169efcbe624932a2'), '7ce88e75')
  assert.equal(m.shortHash(''), '—')
  assert.equal(m.shortHash(undefined), '—')
})

test('示例文件是合法 JSON，用的是后端认的写法', () => {
  const doc = JSON.parse(m.HOOKS_EXAMPLE)
  const pre = doc.hooks.PreToolUse[0]
  assert.equal(pre.matcher, 'Bash')
  assert.equal(pre.hooks[0].type, 'command')
  assert.equal(pre.hooks[0].onFailure, 'block')
  assert.equal(pre.hooks[0].timeout, 10)
  assert.equal(pre.hooks[0].command, 'python D:\\tools\\guard.py')
  assert.equal(doc.hooks.Stop[0].hooks[0].type, 'command')
  for (const event of Object.keys(doc.hooks)) assert.ok(m.HOOK_EVENTS.some(e => e.id === event), `未知事件 ${event}`)
})
