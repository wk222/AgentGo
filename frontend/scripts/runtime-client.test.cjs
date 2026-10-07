// No additional test dependency: compile the shared client with installed TS.
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')
const file = path.resolve(__dirname, '../../frontend-shared/runtime-client.ts')
const compiled = ts.transpileModule(fs.readFileSync(file, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText
const mod = new Module(file)
mod._compile(compiled, file)
const { RuntimeClient } = mod.exports

test('session isolation, duplicates, late delivery and resumed segments', () => {
  let listener, sid = 'one', disposed = false
  const client = new RuntimeClient({ call: async () => {}, on: (_, fn) => { listener = fn; return () => { disposed = true } } })
  const got = []
  const stop = client.subscribe(() => sid, ev => got.push(ev.type))
  const emit = (seq, type, session_id = 'one', run_id = 'r') => listener({ run_id, session_id, seq, type })
  emit(1, 'token', 'other')
  emit(1, 'token'); emit(1, 'token'); emit(2, 'done'); emit(1, 'token')
  emit(3, 'status'); emit(4, 'token'); emit(5, 'done')
  sid = 'two'; emit(6, 'token'); emit(1, 'token', 'two', 'next')
  assert.deepEqual(got, ['token', 'done', 'status', 'token', 'done', 'token'])
  stop(); assert.equal(disposed, true)
})

test('failed starts reject and cancellation uses unified endpoint', async () => {
  const calls = []
  const client = new RuntimeClient({ call: async (...args) => { calls.push(args); return { success: false, error: 'offline' } }, on: () => () => {} })
  await assert.rejects(client.start('s', 'hello'), /offline/)
  await client.cancel('s')
  assert.deepEqual(calls, [['RunEngine', '', 's', 'hello', []], ['CancelEngineSession', 's']])
})
