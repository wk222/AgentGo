// 「聊天软件」卡片的纯逻辑单测：状态一句话、白名单解析、提交内容里密钥留空不上送、扫码阶段文案。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/features/settings/imModel.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const m = mod.exports

const tg = { id: 'telegram', label: 'Telegram', qr_login: false, fields: [{ key: 'bot_token', label: 'Bot Token', secret: true }, { key: 'note', label: '备注', secret: false }] }
const wx = { id: 'weixin', label: '微信', qr_login: true, fields: null }
const ch = (over = {}) => ({ platform: 'telegram', enabled: true, running: true, allow_all: false, allow_from: ['1'], fields: {}, secrets_set: {}, logged_in: false, ...over })

test('状态：没配置、未启用、问题、缺白名单、没连上、运行中', () => {
  assert.equal(m.channelSummary(tg, undefined).tone, 'off')
  assert.equal(m.channelSummary(tg, ch({ enabled: false })).text, '已配置，未启用')
  const bad = m.channelSummary(tg, ch({ problems: ['bot_token is required'] }))
  assert.equal(bad.tone, 'bad')
  assert.match(bad.text, /bot_token is required/)
  const noAllow = m.channelSummary(tg, ch({ allow_from: [] }))
  assert.equal(noAllow.tone, 'warn')
  assert.match(noAllow.text, /拒绝/)
  assert.equal(m.channelSummary(tg, ch({ running: false })).tone, 'warn')
  assert.equal(m.channelSummary(tg, ch()).tone, 'ok')
  assert.match(m.channelSummary(tg, ch({ allow_all: true })).text, /任何人/)
})

test('微信没登录时提示扫码，而不是显示运行中', () => {
  const s = m.channelSummary(wx, ch({ platform: 'weixin', logged_in: false }))
  assert.equal(s.tone, 'warn')
  assert.match(s.text, /扫码/)
  assert.equal(m.channelSummary(wx, ch({ platform: 'weixin', logged_in: true })).tone, 'ok')
})

test('白名单文本：逗号、分号、中文标点、空白都能分，去重去空', () => {
  assert.deepEqual(m.parseAllowFrom('1, 2；3\n4  2'), ['1', '2', '3', '4'])
  assert.deepEqual(m.parseAllowFrom('  '), [])
  assert.equal(m.formatAllowFrom(['a', 'b']), 'a\nb')
  assert.equal(m.formatAllowFrom(undefined), '')
})

test('提交内容：密钥留空不上送，非密钥留空照送（后端据此清除）', () => {
  const req = m.buildSaveRequest(tg, { enabled: true, allowAll: false, allowFrom: '7, 8', values: { bot_token: '  ', note: '' } })
  assert.deepEqual(req.fields, { note: '' })
  assert.deepEqual(req.allow_from, ['7', '8'])
  const withSecret = m.buildSaveRequest(tg, { enabled: true, allowAll: true, allowFrom: '', values: { bot_token: ' abc ' } })
  assert.equal(withSecret.fields.bot_token, 'abc')
  assert.equal(withSecret.allow_all, true)
  assert.deepEqual(m.buildSaveRequest(wx, { enabled: true, allowAll: false, allowFrom: '', values: {} }).fields, {})
})

test('扫码阶段', () => {
  assert.equal(m.loginActive({ status: 'waiting' }), true)
  assert.equal(m.loginActive({ status: 'scanned' }), true)
  assert.equal(m.loginActive({ status: 'done' }), false)
  assert.equal(m.loginText({ status: 'error', message: 'x' }), 'x')
  assert.equal(m.loginText({ status: 'scanned' }), '已扫码，请在手机上确认')
})
