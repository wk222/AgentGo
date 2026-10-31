import type { CallTraceEvent } from '../../shared/ipc/types'

export interface TracedCall {
  callId: string
  runId: string
  tool: string
  steps: CallTraceEvent[]
  failed: boolean
  tookMs: number
  at: number
}

export interface TraceView {
  calls: TracedCall[]
  /** 整次运行级的步骤（上下文压缩、模型用量），按运行归类 */
  runSteps: Record<string, CallTraceEvent[]>
}

const STAGE_ORDER: Record<string, number> = { model_args: 0, args_repaired: 1, policy: 2, result: 3 }

export const STAGE_LABEL: Record<string, string> = {
  model_args: '模型原样写的参数',
  args_repaired: '参数修复',
  policy: '策略决定',
  result: '返回结果',
  compaction: '上下文压缩',
  usage: '模型用量',
}

/** 把一串步骤按“调用”归并（最新的调用在前），运行级步骤按 run_id 单独放。 */
export function buildTraceView(events: CallTraceEvent[]): TraceView {
  const byCall = new Map<string, TracedCall>()
  const runSteps: Record<string, CallTraceEvent[]> = {}
  for (const e of events) {
    if (!e.call_id) {
      ;(runSteps[e.run_id] ||= []).push(e)
      continue
    }
    const key = `${e.run_id}|${e.call_id}`
    let c = byCall.get(key)
    if (!c) {
      c = { callId: e.call_id, runId: e.run_id, tool: e.tool || '', steps: [], failed: false, tookMs: 0, at: e.at }
      byCall.set(key, c)
    }
    if (!c.tool && e.tool) c.tool = e.tool
    c.steps.push(e)
    if (e.stage === 'result') {
      c.tookMs = e.duration_ms || 0
      if (e.data?.failed === true || e.data?.refused_by || e.data?.rejected) c.failed = true
    }
    if (e.stage === 'policy' && /deny|reject/.test(String(e.data?.action || ''))) c.failed = true
    if (e.stage === 'args_repaired' && e.data?.rejected) c.failed = true
  }
  const calls = [...byCall.values()]
  for (const c of calls) c.steps.sort((a, b) => (STAGE_ORDER[a.stage] ?? 9) - (STAGE_ORDER[b.stage] ?? 9) || a.seq - b.seq)
  calls.sort((a, b) => b.at - a.at || b.steps[0].seq - a.steps[0].seq)
  return { calls, runSteps }
}

/** 一步的一句话摘要，细节在展开里看。 */
export function stepSummary(e: CallTraceEvent): string {
  const d = e.data || {}
  switch (e.stage) {
    case 'model_args':
      return clip(String(d.args ?? ''), 80)
    case 'args_repaired':
      return d.rejected ? `被拒绝：${clip(String(d.rejected), 60)}` : '修复后与原参数不同'
    case 'policy':
      return [d.action, d.risk, d.result].filter(Boolean).join(' · ')
    case 'result': {
      if (d.refused_by) return `连续失败止损：未执行`
      if (d.paused) return '暂停等待用户'
      if (d.aborted) return `已中止：${clip(String(d.error ?? ''), 50)}`
      return `${d.failed ? '失败' : '成功'}${e.duration_ms ? ` · ${e.duration_ms} ms` : ''}${d.result_chars ? ` · ${d.result_chars} 字` : ''}`
    }
    case 'compaction':
      return d.applied
        ? `${d.tokens_before} → ${d.tokens_after} tokens`
        : `未采用（${clip(String(d.reason ?? ''), 40)}）${d.tokens_before} → ${d.tokens_after}`
    case 'usage':
      return `输入 ${d.prompt_tokens ?? 0}（缓存 ${d.cached_tokens ?? 0}） · 输出 ${d.completion_tokens ?? 0}`
    default:
      return ''
  }
}

function clip(s: string, n: number): string {
  const t = s.replace(/\s+/g, ' ').trim()
  return t.length > n ? `${t.slice(0, n)}…` : t
}
