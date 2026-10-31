// 按轮回退的纯展示逻辑：哪条消息能回退、确认框怎么说、结果怎么告诉用户。
// 不碰 Vue 与 IPC，单测见 scripts/turn-checkpoint-model.test.cjs。
import type { Message } from './useChat'

export type RollbackAction = 'restore' | 'recreate' | 'delete'

/** Go: workspace.RollbackChange */
export interface RollbackChange {
  path: string
  name: string
  action: RollbackAction
  before_size: number
  after_size: number
  /** 回退会放过这个文件（原内容没保存，或现有内容太大无法先备份）。 */
  skip: boolean
  reason?: string
}

/** Go: workspace.CheckpointInfo */
export interface TurnCheckpoint {
  id: string
  kind: string
  seq: number
  prompt?: string
  created_at: number
  files: number
  skipped: number
  root: string
}

/** Go: workspace.RollbackPreview（计数是完整的，changes 最多带 200 条） */
export interface RollbackPreview {
  checkpoint: TurnCheckpoint
  changes: RollbackChange[]
  restore: number
  recreate: number
  delete: number
  skipped: number
  unverified: number
}

export interface RollbackIssue {
  path: string
  reason: string
}

/** Go: workspace.RollbackResult */
export interface RollbackResult {
  restored: number
  recreated: number
  deleted: number
  skipped: RollbackIssue[]
  failed: RollbackIssue[]
  unverified: number
  undo_id?: string
}

export type Tone = 'success' | 'info' | 'warning' | 'error'
export interface Notice {
  tone: Tone
  text: string
}

/** 回退后写进对话的说明行（Go: noteKindRollback）。 */
export const ROLLBACK_NOTE_KIND = 'workspace_rollback'

const LIST_LIMIT = 10
const SKIP_LIST_LIMIT = 3

/** 用户消息上的回退点编号；其他消息没有。 */
export function checkpointOf(msg: Pick<Message, 'role' | 'meta'> | null | undefined): string {
  if (!msg || msg.role !== 'user') return ''
  return String(msg.meta?.checkpoint || '').trim()
}

export function isRollbackNote(msg: Pick<Message, 'role' | 'meta'> | null | undefined): boolean {
  return !!msg && msg.role === 'assistant' && msg.meta?.kind === ROLLBACK_NOTE_KIND
}

/** 说明行在界面上只画第一句；整段话（给模型读的）放在悬浮提示里。 */
export function noteHeadline(content: string | undefined): string {
  const text = String(content || '').replace(/^【系统记录】/, '').trim()
  const end = text.indexOf('）。')
  if (end > 0) return text.slice(0, end + 1)
  const dot = text.indexOf('。')
  return dot > 0 ? text.slice(0, dot) : text
}

/**
 * 会话行（GetSessionMessages）→ 消息 meta。回退点编号只认用户消息，说明行只认
 * 约定的 kind，别的键不放进来。
 */
export function rowMeta(row: any): Record<string, any> | undefined {
  const meta: Record<string, any> = {}
  if (row?.images) meta.images = row.images
  const checkpoint = typeof row?.checkpoint === 'string' ? row.checkpoint.trim() : ''
  if (checkpoint && row?.role === 'user') meta.checkpoint = checkpoint
  if (row?.kind === ROLLBACK_NOTE_KIND) meta.kind = ROLLBACK_NOTE_KIND
  return Object.keys(meta).length ? meta : undefined
}

/**
 * 一轮开始时（status=started）后端告诉前端这一轮的回退点：把编号记到这一轮的
 * 用户消息上。只在这一页自己发起了这一轮（initiatedHere）、且 segStart 正是一条
 * 还没有回退点的用户文字消息时才打标——别处发起的运行（IM、自动化）到达这里时
 * segStart 是过期的，宁可不打（重新载入会话后编号会从消息里读出来），也不能
 * 把回退点挂到另一条消息上。返回 tagged / failed（建点失败，原因在载荷里）/ none。
 */
export function tagStartedCheckpoint(
  msgs: Message[],
  segStart: number,
  payload: Record<string, any> | undefined,
  initiatedHere: boolean,
): 'tagged' | 'failed' | 'none' {
  const id = String(payload?.checkpoint_id ?? '').trim()
  const error = String(payload?.checkpoint_error ?? '').trim()
  if (!initiatedHere || (!id && !error)) return 'none'
  const target = msgs[segStart]
  if (!target || target.role !== 'user' || (target.type && target.type !== 'text')) return 'none'
  if (target.meta?.checkpoint) return 'none'
  if (!id) return 'failed'
  target.meta = { ...(target.meta || {}), checkpoint: id }
  return 'tagged'
}

/** 建点失败时给用户的话（对话本身不受影响）。 */
export function checkpointWarning(error: string): string {
  const raw = String(error || '').trim()
  let why = raw || '未知原因'
  if (/paused/.test(raw)) why = '工作区文件太多或扫描太慢，回退点暂停约 10 分钟'
  else if (/too many files/.test(raw)) why = '工作区文件太多'
  else if (/deadline exceeded/.test(raw)) why = '扫描工作区超时'
  return `这一轮没能建立回退点，之后无法回到它之前（对话本身不受影响）：${why}`
}

/** ListTurnCheckpoints 的回答 → 还能回退的编号集合。 */
export function liveCheckpointIds(reply: { checkpoints?: Array<{ id?: string }> } | null | undefined): Record<string, true> {
  const out: Record<string, true> = {}
  for (const c of reply?.checkpoints || []) {
    const id = String(c?.id || '').trim()
    if (id) out[id] = true
  }
  return out
}

/** 这条消息现在要不要显示「回到这一轮之前」：有回退点、回退点还在、没有在运行。 */
export function canRollback(
  msg: Pick<Message, 'role' | 'meta'> | null | undefined,
  live: Record<string, true>,
  sending: boolean,
): boolean {
  const id = checkpointOf(msg)
  return !!id && !!live[id] && !sending
}

export function skipReasonText(reason: string | undefined): string {
  const r = String(reason || '')
  if (r.includes('too large to keep a safety copy')) return '现有内容太大，没法先备份，替换后就撤销不了'
  if (r.includes('not kept')) return '体积过大或超出内存预算，当时没有保存原内容'
  if (r.includes('could not be read')) return '当时无法读取'
  return r || '无法处理'
}

function countsText(restore: number, recreate: number, del: number): string {
  const parts: string[] = []
  if (restore) parts.push(`恢复 ${restore} 个`)
  if (recreate) parts.push(`找回 ${recreate} 个`)
  if (del) parts.push(`删除 ${del} 个`)
  return parts.join('、')
}

const ACTION_LABEL: Record<RollbackAction, string> = { restore: '恢复', recreate: '找回', delete: '删除' }

/** 确认框的正文。empty = 没有任何可回退的文件，不必弹确认。 */
export function describeRollback(p: RollbackPreview | null | undefined): { text: string; empty: boolean } {
  if (!p) return { text: '没有拿到回退预览', empty: true }
  const total = p.restore + p.recreate + p.delete
  const skipped = (p.changes || []).filter(c => c.skip)
  const skippedBlock = (): string[] => {
    if (!p.skipped) return []
    const lines = [`有 ${p.skipped} 个文件不会动：`]
    for (const c of skipped.slice(0, SKIP_LIST_LIMIT)) lines.push(`  ${c.path}：${skipReasonText(c.reason)}`)
    if (p.skipped > SKIP_LIST_LIMIT) lines.push(`  …… 还有 ${p.skipped - Math.min(SKIP_LIST_LIMIT, skipped.length)} 个`)
    return lines
  }
  if (total === 0) {
    const lines = p.skipped
      ? ['没有可以回退的文件。', '', ...skippedBlock()]
      : ['工作区文件和这一轮开始之前一致，没有需要回退的内容。']
    return { text: lines.join('\n'), empty: true }
  }

  const prompt = String(p.checkpoint?.prompt || '').trim()
  const lines = [`回到${prompt ? `「${prompt}」` : '这一轮'}开始之前：`, '', `${countsText(p.restore, p.recreate, p.delete)}（共 ${total} 个文件）。`, '']
  const actionable = (p.changes || []).filter(c => !c.skip)
  for (const c of actionable.slice(0, LIST_LIMIT)) lines.push(`  ${ACTION_LABEL[c.action] || c.action}  ${c.path}`)
  const hidden = total - Math.min(LIST_LIMIT, actionable.length)
  if (hidden > 0) lines.push(`  …… 还有 ${hidden} 个`)
  const skipLines = skippedBlock()
  if (skipLines.length) lines.push('', ...skipLines)
  lines.push(
    '',
    '这期间你手动改过的文件也会一起恢复原样。回退前会先自动保存现在的状态，之后可以点「撤销这次回退」。',
    '只处理工作区里被跟踪的文件：.git、node_modules 等目录，以及超过 8 MB 的大文件不在其内。',
  )
  return { text: lines.join('\n'), empty: false }
}

/** 后端说"现在忙"的原因（英文）→ 中文。 */
export function busyText(raw: string): string {
  return /approval/.test(String(raw || '')) ? '有请求正在等你批准，请先处理' : '有任务正在运行，请先停止或等它结束'
}

/** 预览 / 回退失败（没有结果）时的提示。 */
export function rollbackError(reply: { error?: string; busy?: boolean | string } | null | undefined): Notice {
  const raw = String(reply?.error || (typeof reply?.busy === 'string' ? reply.busy : '') || '').trim()
  if (reply?.busy || /in progress|waiting for approval/.test(raw)) return { tone: 'warning', text: `现在不能回退：${busyText(raw)}` }
  if (/checkpoint not found/.test(raw)) {
    return { tone: 'warning', text: '这一轮的回退点已经失效（应用重启过，或被更新的回退点替换了），无法回到它之前' }
  }
  if (/another workspace/.test(raw)) return { tone: 'warning', text: '工作区已经切换过，这一轮的回退点不再适用' }
  return { tone: 'error', text: `回退失败：${raw || '未知错误'}` }
}

/** 做了什么的一句话：提示与「撤销」条共用。 */
export function rollbackSummary(r: RollbackResult | null | undefined): string {
  return countsText(r?.restored || 0, r?.recreated || 0, r?.deleted || 0)
}

type RollbackReply = { success?: boolean; error?: string; busy?: boolean; result?: RollbackResult }

/**
 * RollbackToTurn 的回答 → 给用户的提示。undo = 这次是在撤销上一次回退：
 * 措辞换成"撤销"，而且不再提示"可以撤销"（撤销本身不再给撤销条）。
 */
export function rollbackNotice(reply: RollbackReply | null | undefined, undo = false): Notice {
  const r = reply?.result
  if (!r) return rollbackError(reply)
  const done = r.restored + r.recreated + r.deleted
  const skipped = r.skipped?.length || 0
  const failed = r.failed || []
  if (failed.length) {
    const first = failed[0]
    const tail = undo ? '请检查这些文件。' : '可以点「撤销这次回退」还原。'
    return { tone: 'warning', text: `已处理 ${done} 个文件，但有 ${failed.length} 个没能写回（${first.path}：${first.reason}）。${tail}` }
  }
  if (done === 0) {
    return skipped
      ? { tone: 'info', text: `没有文件被${undo ? '撤销' : '回退'}：有 ${skipped} 个文件无法处理，保持原样` }
      : { tone: 'info', text: undo ? '工作区文件已经是回退之前的样子，没有改动' : '工作区文件已经和这一轮开始之前一致，没有改动' }
  }
  const tail = skipped ? `；另有 ${skipped} 个文件无法处理，保持原样` : ''
  return { tone: 'success', text: `${undo ? '已撤销上一次回退' : '已回到这一轮开始之前'}：${rollbackSummary(r)}${tail}` }
}

/**
 * 撤销条该不该收起：撤销真的执行过（哪怕个别文件没写回）或回退点已经没了，就收起；
 * 只是"现在忙"之类的拒绝，留着让用户稍后再点。
 */
export function undoSettled(reply: RollbackReply | null | undefined): boolean {
  if (reply?.result) return true
  return /checkpoint not found|another workspace/.test(String(reply?.error || ''))
}
