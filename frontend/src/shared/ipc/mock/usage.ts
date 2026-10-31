// 预览模式（浏览器直接打开前端）下的用量与诊断数据，形状与 Go 的 GetUsageSummary / GetAppInfo / ExportDiagnostics 一致。
import type { AppInfo, DiagnosticsExport, UsageByDay, UsageByModel, UsageSummary, UsageTotals } from '../types'

const pad = (n: number) => String(n).padStart(2, '0')
const dateKey = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`

function totals(calls: number, prompt: number, cached: number, completion: number, reasoning: number): UsageTotals {
  return {
    calls,
    prompt_tokens: prompt,
    cached_tokens: cached,
    completion_tokens: completion,
    reasoning_tokens: reasoning,
    total_tokens: prompt + completion,
    cache_hit_rate: prompt > 0 ? cached / prompt : 0,
  }
}

export function mockUsageSummary(days: number): UsageSummary {
  const n = Math.max(1, Math.min(90, Math.floor(days) || 7))
  const today = new Date()
  const byDay: UsageByDay[] = []
  for (let i = n - 1; i >= 0; i--) {
    const d = new Date(today.getFullYear(), today.getMonth(), today.getDate() - i)
    // 周末几乎不用；工作日用量起伏，便于看出柱状图的样子。
    const weekend = d.getDay() === 0 || d.getDay() === 6
    const k = weekend ? 0 : 3 + ((i * 7) % 5)
    byDay.push({ date: dateKey(d), ...totals(k * 4, k * 41000, k * 26000, k * 3200, k * 900) })
  }
  const sum = (f: (t: UsageTotals) => number) => byDay.reduce((a, d) => a + f(d), 0)
  const all = totals(sum(d => d.calls), sum(d => d.prompt_tokens), sum(d => d.cached_tokens), sum(d => d.completion_tokens), sum(d => d.reasoning_tokens))
  const share = (name: string, part: number): UsageByModel => ({
    model: name,
    ...totals(Math.round(all.calls * part), Math.round(all.prompt_tokens * part), Math.round(all.cached_tokens * part * 1.1), Math.round(all.completion_tokens * part), Math.round(all.reasoning_tokens * part)),
  })
  return {
    days: n,
    from: byDay[0].date,
    to: byDay[byDay.length - 1].date,
    totals: all,
    by_model: [share('qwen/qwen3.7-max', 0.7), share('deepseek-v4-flash', 0.3)],
    by_day: byDay,
  }
}

export const mockAppInfo: AppInfo = {
  version: 'preview',
  go_version: 'go1.26',
  os: 'windows',
  os_version: '10.0.26300',
  arch: 'amd64',
  tags: 'web',
  host: 'web',
  data_dir: 'C:\\Users\\preview\\AppData\\Roaming\\AgentGo',
}

export const mockDiagnostics: DiagnosticsExport = {
  name: 'agentgo-diagnostics-preview.zip',
  path: 'C:\\Users\\preview\\AppData\\Roaming\\AgentGo\\attachments\\agentgo-diagnostics-preview.zip',
  size: 48213,
  url: '#',
  files: ['README.txt', 'environment.json', 'llm.json', 'features.json', 'config.json', 'plugins.json', 'tools.json', 'usage.json', 'database.json', 'logs/agentgo.log.tail.txt'],
}
