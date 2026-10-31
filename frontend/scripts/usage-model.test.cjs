// 「用量」页的纯展示逻辑单测：数字读法、命中率、柱状图高度与横轴标签。
const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const Module = require('node:module')
const ts = require('typescript')

const file = path.resolve(__dirname, '../src/features/settings/usageModel.ts')
const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText
const mod = new Module(file)
mod._compile(out, file)
const m = mod.exports

test('Token 数用万/亿读：小数字加千分位，大数字不出现 K/M', () => {
  assert.equal(m.formatTokens(0), '0')
  assert.equal(m.formatTokens(-5), '0')
  assert.equal(m.formatTokens(NaN), '0')
  assert.equal(m.formatTokens(987), '987')
  assert.equal(m.formatTokens(9876), '9,876')
  assert.equal(m.formatTokens(12800), '1.3 万')
  assert.equal(m.formatTokens(10000), '1 万')
  assert.equal(m.formatTokens(125000), '12.5 万')
  assert.equal(m.formatTokens(1250000), '125 万')
  assert.equal(m.formatTokens(34000000), '3400 万')
  assert.equal(m.formatTokens(100000000), '1 亿')
  assert.equal(m.formatTokens(3400000000), '34 亿')
  assert.equal(m.formatTokens(99999999), '1 亿')
})

test('命中率：取整、夹在 0~100、没有输入时不假装有比例', () => {
  assert.equal(m.formatPercent(0.5766), '58%')
  assert.equal(m.formatPercent(0), '0%')
  assert.equal(m.formatPercent(1.4), '100%')
  assert.equal(m.formatPercent(-1), '0%')
  assert.equal(m.formatPercent(0.5, false), '—')
  assert.equal(m.formatPercent(NaN), '—')
})

test('文件大小', () => {
  assert.equal(m.formatBytes(0), '0 B')
  assert.equal(m.formatBytes(900), '900 B')
  assert.equal(m.formatBytes(1536), '1.5 KB')
  assert.equal(m.formatBytes(48213), '47 KB')
  assert.equal(m.formatBytes(5 * 1024 * 1024), '5 MB')
  assert.equal(m.formatBytes(-1), '—')
})

test('区间名称：预设用中文名，其它天数照实写', () => {
  assert.equal(m.rangeLabel(1), '今天')
  assert.equal(m.rangeLabel(7), '近 7 天')
  assert.equal(m.rangeLabel(30), '近 30 天')
  assert.equal(m.rangeLabel(14), '近 14 天')
  assert.deepEqual(m.USAGE_RANGES.map(r => r.days), [1, 7, 30])
})

const day = (date, total, calls = 1) => ({ date, total_tokens: total, calls })

test('柱状图：相对最高日，有用量的日子至少留一点高度，全空不除零', () => {
  const bars = m.dayBars([day('2026-10-25', 1000), day('2026-10-26', 0), day('2026-10-27', 4000, 9)])
  assert.deepEqual(bars.map(b => b.height), [0.25, 0, 1])
  assert.equal(bars[2].calls, 9)
  const tiny = m.dayBars([day('2026-10-26', 1), day('2026-10-27', 100000)])
  assert.equal(tiny[0].height, 0.04)
  assert.deepEqual(m.dayBars([day('2026-10-27', 0)]).map(b => b.height), [0])
  assert.deepEqual(m.dayBars([]), [])
})

test('柱状图横轴：短区间每天标注，长区间只标首尾与整周', () => {
  const short = m.dayBars(['25', '26', '27'].map(d => day(`2026-10-${d}`, 1)))
  assert.deepEqual(short.map(b => b.label), ['10/25', '10/26', '10/27'])
  const long = m.dayBars(Array.from({ length: 30 }, (_, i) => day(`2026-10-${String(i + 1).padStart(2, '0')}`, 1)))
  const labelled = long.map((b, i) => (b.label ? i : -1)).filter(i => i >= 0)
  assert.deepEqual(labelled, [0, 7, 14, 21, 28, 29])
  assert.equal(long[0].label, '10/1')
})

test('缓存说明：没有输入/没命中/部分/过半', () => {
  assert.match(m.cacheHint(0, 0), /没有返回输入/)
  assert.match(m.cacheHint(1000, 0), /没有命中缓存/)
  assert.match(m.cacheHint(1000, 200), /部分输入/)
  assert.match(m.cacheHint(1000, 800), /过半/)
})
