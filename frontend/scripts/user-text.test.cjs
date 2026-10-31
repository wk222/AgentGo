// 斜杠命令展开后的用户消息怎么显示：气泡里只留用户敲的原话，说明书折叠掉。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/features/chat/userText.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const { userDisplayText } = mod.exports

const block = '<command-instructions name="review">\n用户用 /review 命令请求代码审查。\n## 结论\n</command-instructions>'

test('普通消息原样显示，空值得到空串', () => {
  assert.equal(userDisplayText('帮我看下 @a.go'), '帮我看下 @a.go')
  assert.equal(userDisplayText('  保留首尾空白  '), '  保留首尾空白  ')
  assert.equal(userDisplayText(''), '')
  assert.equal(userDisplayText(undefined), '')
  assert.equal(userDisplayText(null), '')
})

test('/review 展开后只显示用户敲的那一行', () => {
  assert.equal(userDisplayText('/review 并发安全\n\n' + block), '/review 并发安全')
  assert.equal(userDisplayText('/review\n\n' + block), '/review')
})

test('命令参数里的多行文字完整保留', () => {
  assert.equal(userDisplayText('/review 看 @a.go\n再看错误处理\n\n' + block), '/review 看 @a.go\n再看错误处理')
})

test('@ 提及追加的上下文不在这里处理，原样保留在说明书之后', () => {
  const tail = '\n\n---\n### [Auto-Injected Context via @ Mentions]\nfile body'
  assert.equal(userDisplayText('/review @a.go\n\n' + block + tail), '/review @a.go' + tail)
})

test('只认成对的说明书标签，没闭合的不动', () => {
  const broken = '/review\n\n<command-instructions name="review">\n没有结束标签'
  assert.equal(userDisplayText(broken), broken)
})
