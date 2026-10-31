const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')
const file = path.resolve(__dirname, '../src/features/chat/evidenceModel.ts')
const mod = new Module(file)
mod._compile(ts.transpileModule(fs.readFileSync(file, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText, file)
const { evidenceForMessage, evidenceLocation } = mod.exports
const id = 'ev_' + 'a'.repeat(32)
const revision = 'b'.repeat(64)
const citation = { id, kind: 'file', source: '报告.xlsx', revision, start: 3, end: 6, source_spans: [{ sheet: '汇总', row_start: 7, row_end: 8 }] }
const value = { citation, text: '中😀文', offset: 3, source: { path: citation.source, revision } }
const user = { role: 'user', type: 'text', content: '分析' }
const answer = { role: 'assistant', type: 'text', content: `结果 [[${id}]]` }
const tool = (data = value, extra = {}) => ({ role: 'assistant', type: 'tool_call', toolName: 'parse_file', status: 'success', content: JSON.stringify(data), ...extra })

test('accepts only actual preceding source results, with Unicode rune offsets', () => {
  const refs = evidenceForMessage([user, tool(), answer], 2)
  assert.equal(refs.get(id).text, '中😀文')
  assert.equal(evidenceLocation(refs.get(id)), '汇总 · 第 7–8 行')
  assert.equal(evidenceForMessage([user, answer, tool()], 1).size, 0)
})
test('prior turns, text, wrong tools, failed results and malformed JSON cannot authorize a citation', () => {
  for (const messages of [
    [user, tool(), user, answer],
    [user, { ...tool(), type: 'text' }, answer],
    [user, tool(value, { toolName: 'web_search' }), answer],
    [user, tool(value, { status: 'error' }), answer],
    [user, tool(value, { content: '{"citation":' }), answer],
  ]) assert.equal(evidenceForMessage(messages, messages.length - 1).size, 0)
})
test('rejects source/revision/offset mismatches and unread ranges', () => {
  for (const data of [
    { ...value, offset: 0 },
    { ...value, text: '中😀' },
    { ...value, source: { path: 'other', revision } },
    { ...value, citation: { ...citation, revision: 'bad' } },
    { ...value, citation: { ...citation, id: 'invented' } },
  ]) assert.equal(evidenceForMessage([user, tool(data), answer], 2).size, 0)
})
test('web errors are not page evidence; source strings remain display data', () => {
  const web = { citation: { ...citation, kind: 'web', source: 'https://example.com' }, text: value.text, offset: 3, url: 'https://example.com', revision, status: 'ok' }
  const msg = data => tool({ results: [data] }, { toolName: 'web_fetch' })
  assert.equal(evidenceForMessage([user, msg(web), answer], 2).get(id).source, web.url)
  assert.equal(evidenceForMessage([user, msg({ ...web, status: 'error' }), answer], 2).size, 0)
  assert.equal(evidenceLocation({ spans: [], start: 0, end: 9 }), '解析文本字符 0–9')
})

test('stored snapshots remain readable in their historical turn without granting a later turn evidence', () => {
  const raw = JSON.stringify({ citation, snapshot_id: 'es_' + 'c'.repeat(64), untrusted: true, text: value.text })
  const restored = tool(undefined, { toolName: 'read_evidence', content: raw })
  assert.equal(evidenceForMessage([user, restored, answer], 2).get(id).text, value.text)
  assert.equal(evidenceForMessage([user, restored, answer, user, answer], 4).size, 0)
})
