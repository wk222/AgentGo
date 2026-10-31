// 「用量」页的纯展示逻辑：数字怎么读、柱状图怎么画。不依赖 Vue，方便单测。
import type { UsageByDay } from '../../shared/ipc/types'

/** 办公用户读「万 / 亿」比读 K / M 顺：12800 → 1.3 万，1250000 → 125 万。小于一万的数字加千分位。 */
export function formatTokens(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0'
  if (n < 10000) return String(Math.round(n)).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  const wan = n / 10000
  if (Math.round(wan) >= 10000) {
    const yi = n / 1e8
    return `${yi < 100 ? yi.toFixed(1).replace(/\.0$/, '') : Math.round(yi)} 亿`
  }
  return `${wan < 100 ? wan.toFixed(1).replace(/\.0$/, '') : Math.round(wan)} 万`
}

/** 0.5766 → 58%。没有输入时没有命中率可言，显示 —。 */
export function formatPercent(rate: number, hasInput = true): string {
  if (!hasInput || !Number.isFinite(rate)) return '—'
  return `${Math.round(Math.max(0, Math.min(1, rate)) * 100)}%`
}

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '—'
  if (n < 1024) return `${Math.round(n)} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0).replace(/\.0$/, '')} KB`
  return `${(n / 1024 / 1024).toFixed(1).replace(/\.0$/, '')} MB`
}

export const USAGE_RANGES = [
  { days: 1, label: '今天' },
  { days: 7, label: '近 7 天' },
  { days: 30, label: '近 30 天' },
] as const

export function rangeLabel(days: number): string {
  return USAGE_RANGES.find(r => r.days === days)?.label ?? `近 ${days} 天`
}

export interface DayBar {
  date: string
  /** 横轴文字：10/27；跨度长时只给整周和首尾标，免得挤成一团。 */
  label: string
  total: number
  calls: number
  /** 相对最高一天的高度，0~1；有用量的日子至少留一点可见高度。 */
  height: number
}

export function dayBars(days: UsageByDay[]): DayBar[] {
  const max = days.reduce((m, d) => Math.max(m, d.total_tokens), 0)
  return days.map((d, i) => {
    const [, month, day] = d.date.split('-')
    const showLabel = days.length <= 10 || i === 0 || i === days.length - 1 || i % 7 === 0
    return {
      date: d.date,
      label: showLabel && month && day ? `${Number(month)}/${Number(day)}` : '',
      total: d.total_tokens,
      calls: d.calls,
      height: max > 0 && d.total_tokens > 0 ? Math.max(0.04, d.total_tokens / max) : 0,
    }
  })
}

/** 一句话说明缓存命中是什么意思，只在有输入量时给。 */
export function cacheHint(prompt: number, cached: number): string {
  if (prompt <= 0) return '服务商没有返回输入 Token 时，这里无法统计缓存。'
  const rate = cached / prompt
  if (rate >= 0.5) return '输入内容中过半命中服务商缓存：重复的上下文没有重复计费或重复计算。'
  if (rate > 0) return '部分输入命中服务商缓存。同一个会话里连续追问，命中会逐步升高。'
  return '没有命中缓存：服务商可能不支持提示缓存，或者每次对话的开头内容都不同。'
}
