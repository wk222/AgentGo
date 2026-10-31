// 「运行中」视图的纯展示逻辑：把后端的 GetRunningWork 变成人话、徽标和按钮。
// 不依赖 Vue，也不 import 运行时模块（单测直接转译本文件）。
import type { RunningWork, RunningWorkItem } from '../shared/ipc/types'
import type { RunBadge } from './chatRun'

export const emptyWork = (): RunningWork => ({ items: [], running: 0, waiting: 0, at: 0 })

export const workTotal = (w: Pick<RunningWork, 'running' | 'waiting'>) => w.running + w.waiting

/** 状态栏按钮上的字：既有在跑的也有等你的，两个数都给。 */
export function chipText(w: Pick<RunningWork, 'running' | 'waiting'>): string {
  if (w.waiting > 0 && w.running > 0) return `运行中 ${w.running} · 待处理 ${w.waiting}`
  if (w.waiting > 0) return `待处理 ${w.waiting}`
  if (w.running > 0) return `运行中 ${w.running}`
  return '没有运行中的工作'
}

/** attention：有东西停着等你；busy：都在自己跑；idle：什么都没有。 */
export type ChipTone = 'attention' | 'busy' | 'idle'
export function chipTone(w: Pick<RunningWork, 'running' | 'waiting'>): ChipTone {
  return w.waiting > 0 ? 'attention' : w.running > 0 ? 'busy' : 'idle'
}

const TASK_KIND_LABELS: Record<string, string> = {
  cron: '定时任务',
  trigger: '事件唤醒',
  swarm: '多智能体协作',
  manual: '后台任务',
  matrix_followup: '团队后续处理',
}
export const taskKindLabel = (kind?: string) => TASK_KIND_LABELS[kind || ''] || '后台任务'

const TASK_STATUS_TEXT: Record<string, string> = {
  queued: '排队中',
  running: '运行中',
  completed: '已完成',
  failed: '失败',
  cancelled: '已取消',
}
export const taskStatusText = (status?: string) => TASK_STATUS_TEXT[status || ''] || status || '未知'

/** 任务列表里一行的颜色类：在跑 / 出错 / 其余（已完成、已取消）。 */
export function taskStatusTone(status?: string): 'running' | 'error' | 'done' {
  if (status === 'queued' || status === 'running') return 'running'
  if (status === 'failed') return 'error'
  return 'done'
}

/** 任务被要求做什么，压成一行（和后端 taskSummary 同一个读法）：协作任务取主题，其余取第一行有字的。 */
export function taskInputSummary(t: { kind?: string; input?: string }): string {
  let text = t.input || ''
  if (t.kind === 'swarm') {
    try {
      const parsed = JSON.parse(text)
      if (parsed && typeof parsed.topic === 'string' && parsed.topic.trim()) text = parsed.topic
    } catch { /* 不是 JSON，按原文 */ }
  }
  for (const line of text.split('\n')) {
    const s = line.trim()
    if (!s) continue
    const chars = Array.from(s)
    return chars.length > 60 ? chars.slice(0, 60).join('') + '…' : s
  }
  return ''
}

/** 数据库里仍写着 running，但本程序没有在跑它：多半是上次程序关掉时被打断的。 */
export function isInterrupted(status: string | undefined, liveTaskIDs: ReadonlySet<string>, id: string): boolean {
  return (status === 'running' || status === 'queued') && !liveTaskIDs.has(id)
}

const WAITING_SOURCE_LABELS: Record<string, string> = {
  workflow: '工作流',
  matrix: '团队协作',
}

/** 没有对应对话、也不是已知内部会话时的名字（对话已被删除，或是后台内部会话）。 */
export function internalSessionLabel(sessionID?: string): string {
  if (sessionID === 'app_matrix_coordinator') return '团队协调（后台）'
  return '后台会话'
}

export function itemTitle(it: RunningWorkItem): string {
  switch (it.kind) {
    case 'task': {
      const label = taskKindLabel(it.task_kind)
      return it.title ? `${label} · ${it.title}` : label
    }
    case 'run':
      return it.engine ? `后台运行 · ${it.engine}` : '后台运行'
    case 'waiting':
      return `${WAITING_SOURCE_LABELS[it.source || ''] || '后台流程'}在等你`
    default:
      return it.known ? (it.title?.trim() || '未命名对话') : internalSessionLabel(it.session_id)
  }
}

export function stateText(it: Pick<RunningWorkItem, 'state'>): string {
  switch (it.state) {
    case 'awaiting_approval': return '等你批准'
    case 'awaiting_answer': return '等你回答'
    case 'queued': return '排队中'
    default: return '运行中'
  }
}

export const isWaiting = (it: Pick<RunningWorkItem, 'state'>) => it.state === 'awaiting_approval' || it.state === 'awaiting_answer'

/** 秒数读成「12 秒」「3 分 5 秒」「2 小时 10 分」「3 天」。 */
export function durationText(seconds: number): string {
  const s = Math.max(0, Math.floor(Number.isFinite(seconds) ? seconds : 0))
  if (s < 60) return `${s} 秒`
  if (s < 3600) {
    const m = Math.floor(s / 60)
    const r = s % 60
    return r ? `${m} 分 ${r} 秒` : `${m} 分钟`
  }
  if (s < 86400) {
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    return m ? `${h} 小时 ${m} 分` : `${h} 小时`
  }
  return `${Math.floor(s / 86400)} 天`
}

/** 「已运行 3 分 5 秒」「已等待 40 秒」；开始时间不明就什么都不说。 */
export function elapsedText(it: Pick<RunningWorkItem, 'state' | 'since'>, nowSec: number): string {
  if (!it.since || !nowSec) return ''
  const d = nowSec - it.since
  if (d < 3) return '刚开始'
  const verb = isWaiting(it) ? '已等待' : it.state === 'queued' ? '已排队' : '已运行'
  return `${verb} ${durationText(d)}`
}

/** 标题下面那一行小字。 */
export function itemDetail(it: RunningWorkItem, nowSec: number): string {
  const parts: string[] = [stateText(it)]
  if (isWaiting(it)) {
    if (it.state === 'awaiting_approval' && it.tool) parts.push(it.tool)
    if ((it.count ?? 0) > 1) parts.push(`${it.count} 项`)
  }
  if (it.kind === 'task' && it.session_title) parts.push(`在「${it.session_title}」里`)
  const elapsed = elapsedText(it, nowSec)
  if (elapsed) parts.push(elapsed)
  return parts.join(' · ')
}

/** 点一下能不能跳到对应对话：要有对话，而且它还在列表里（已删除的、内部的打不开）。 */
export const canJump = (it: Pick<RunningWorkItem, 'session_id' | 'known'>) => !!it.session_id && it.known

export const jumpLabel = (it: Pick<RunningWorkItem, 'state'>) => (isWaiting(it) ? '去处理' : '查看')

export const stopLabel = (it: Pick<RunningWorkItem, 'kind'>) => (it.kind === 'task' ? '取消' : '停止')

/** 会话列表上的圆点：后端知道的运行状态。页面刷新后本地状态是空的，靠它补上。 */
export function remoteBadges(w: Pick<RunningWork, 'items'>): Record<string, RunBadge> {
  const out: Record<string, RunBadge> = {}
  for (const it of w.items) {
    if (!it.session_id) continue
    if (isWaiting(it)) out[it.session_id] = 'awaiting'
    else if (it.state === 'running') out[it.session_id] = 'running'
  }
  return out
}

/** 本页自己发起的运行以本地状态为准，其余用后端的。 */
export function mergeBadges(local: Record<string, RunBadge>, remote: Record<string, RunBadge>): Record<string, RunBadge> {
  return { ...remote, ...local }
}

/** 两次轮询的结果是不是一样（不比较时间戳，免得每 2 秒都重画一遍）。 */
export function sameWork(a: RunningWork, b: RunningWork): boolean {
  if (a.running !== b.running || a.waiting !== b.waiting || a.items.length !== b.items.length) return false
  return JSON.stringify(a.items) === JSON.stringify(b.items)
}

/** 弹层贴着状态栏按钮左边弹出，窗口窄的时候右边会伸出窗外，右侧的「停止」按钮就点不到了。
 *  返回要把弹层横向挪多少像素（负数往左），让它整个落在窗口里，两边各留 margin。
 *  窗口比弹层还窄时，左边优先对齐（标题和对话名比右侧按钮更要紧，且左边总能看到）。 */
export function panelShift(left: number, right: number, viewportWidth: number, margin = 8): number {
  let shift = Math.min(0, viewportWidth - margin - right)
  shift = Math.max(shift, margin - left)
  return Math.round(shift) || 0 // `|| 0`：把 -0 也变成 0
}
