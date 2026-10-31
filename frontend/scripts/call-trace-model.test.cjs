// 调用追踪卡的纯逻辑单测：按调用归并、步骤顺序、出问题的判定、运行级步骤归类、摘要。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/features/settings/callTraceModel.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const m = mod.exports

let seq = 0
const ev = (stage, over = {}) => ({ seq: ++seq, session_id: 's', run_id: 'r1', call_id: 'c1', tool: 'code_view', stage, at: 1000 + seq, data: {}, ...over })

test('按调用归并，步骤按 参数→修复→策略→结果 排', () => {
  const v = m.buildTraceView([ev('result', { duration_ms: 12, data: { failed: false, result_chars: 5 } }), ev('policy', { data: { action: 'tool_invoke' } }), ev('model_args', { data: { args: '{}' } })])
  assert.equal(v.calls.length, 1)
  assert.deepEqual(v.calls[0].steps.map(s => s.stage), ['model_args', 'policy', 'result'])
  assert.equal(v.calls[0].failed, false)
  assert.equal(v.calls[0].tookMs, 12)
})

test('失败、被止损、被策略拒绝、参数被拒都算出问题', () => {
  const failed = m.buildTraceView([ev('result', { data: { failed: true } })]).calls[0]
  const refused = m.buildTraceView([ev('result', { data: { refused_by: 'same_failure_guard' } })]).calls[0]
  const denied = m.buildTraceView([ev('policy', { data: { action: 'policy_deny' } })]).calls[0]
  const badArgs = m.buildTraceView([ev('args_repaired', { data: { rejected: 'not json' } })]).calls[0]
  assert.deepEqual([failed.failed, refused.failed, denied.failed, badArgs.failed], [true, true, true, true])
})

test('运行级步骤按 run 归类，不混进调用；最新的调用在前', () => {
  const v = m.buildTraceView([
    ev('model_args', { call_id: 'a', run_id: 'r1', at: 1 }),
    ev('compaction', { call_id: '', run_id: 'r1', data: { applied: true, tokens_before: 9, tokens_after: 3 } }),
    ev('model_args', { call_id: 'b', run_id: 'r2', at: 5 }),
  ])
  assert.deepEqual(v.calls.map(c => c.callId), ['b', 'a'])
  assert.equal(v.runSteps.r1.length, 1)
  assert.equal(v.calls[1].steps.length, 1)
})

test('同一个 call_id 在不同运行里是两次调用', () => {
  const v = m.buildTraceView([ev('model_args', { run_id: 'r1' }), ev('model_args', { run_id: 'r2' })])
  assert.equal(v.calls.length, 2)
})

test('摘要：压缩前后、用量、止损', () => {
  assert.equal(m.stepSummary(ev('compaction', { data: { applied: true, tokens_before: 9000, tokens_after: 2000 } })), '9000 → 2000 tokens')
  assert.match(m.stepSummary(ev('usage', { data: { prompt_tokens: 100, cached_tokens: 80, completion_tokens: 7 } })), /输入 100（缓存 80）/)
  assert.match(m.stepSummary(ev('result', { data: { refused_by: 'same_failure_guard' } })), /止损/)
  assert.equal(m.stepSummary(ev('model_args', { data: { args: 'x'.repeat(200) } })).length, 81)
})
