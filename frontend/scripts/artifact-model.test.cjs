// 交付文件卡的纯逻辑单测：解析工具结果、状态文案、是否可下载、会话里后来被移除。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/features/chat/artifactModel.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const m = mod.exports

const rec = (over = {}) => ({ success: true, id: 'a1', name: 'report.xlsx', path: 'C:/w/report.xlsx', size: 10, sha256: 'x', version: 1, status: 'new', url: '/attachments/a1', attached: true, ...over })

test('解析：成功结果得到记录，失败/缺字段/非 JSON 得到 null', () => {
  assert.equal(m.parseArtifact(JSON.stringify(rec())).name, 'report.xlsx')
  assert.equal(m.parseArtifact(JSON.stringify({ success: false, error: 'no file' })), null)
  assert.equal(m.parseArtifact(JSON.stringify({ success: true })), null)
  assert.equal(m.parseArtifact('not json'), null)
  assert.equal(m.parseArtifact(''), null)
})

test('文案：新建、新版本、内容未变、已移除', () => {
  const p = o => m.parseArtifact(JSON.stringify(rec(o)))
  assert.equal(m.artifactBadge(p({})), '第 1 版')
  assert.equal(m.artifactBadge(p({ status: 'new_version', version: 3 })), '第 3 版 · 已更新')
  assert.equal(m.artifactBadge(p({ status: 'unchanged', attached: false })), '第 1 版 · 内容未变')
  assert.equal(m.artifactBadge(p({ status: 'removed', attached: false })), '已移除')
})

test('下载：已移除或没有地址的不给下载；内容未变仍可下载', () => {
  const p = o => m.parseArtifact(JSON.stringify(rec(o)))
  assert.equal(m.artifactDownloadable(p({})), true)
  assert.equal(m.artifactDownloadable(p({ status: 'unchanged', attached: false })), true)
  assert.equal(m.artifactDownloadable(p({ status: 'removed' })), false)
  assert.equal(m.artifactDownloadable(p({ url: '' })), false)
})

test('会话里的最新状态：被移除则覆盖卡片，其余保持', () => {
  const card = m.parseArtifact(JSON.stringify(rec()))
  assert.equal(m.mergeLive(card, [{ id: 'a1', status: 'removed' }]).status, 'removed')
  assert.equal(m.mergeLive(card, [{ id: 'a1', status: 'new' }]).status, 'new')
  assert.equal(m.mergeLive(card, [{ id: 'zz', status: 'removed' }]).status, 'new')
  assert.equal(m.mergeLive(card, undefined).status, 'new')
})
