// 会话分叉的纯展示逻辑：谁是分叉、它从哪来、一次分叉结束后该对用户说什么。
// 不碰 Vue 与 IPC，单测见 scripts/fork-model.test.cjs。
import type { ForkSessionReply, SessionSummary } from '../shared/ipc/types'

/** 会话列表里与分叉有关的字段（Go: sessions.Session 的 parent_*，只有分叉才有）。 */
export type ForkFields = Pick<SessionSummary, 'parent_id' | 'parent_title' | 'parent_gone'>

/** ForkSession 的回答（Go: bridge.ForkSession）。busy = 原对话正在运行或停在审批上：不是故障。 */
export type ForkReply = ForkSessionReply & { success?: boolean; error?: string }

export interface ForkLineage {
  parentId: string
  parentTitle: string
  /** 原对话已被删除：不能再跳回去。 */
  gone: boolean
}

/** 这条会话是不是分叉出来的；是则给出来处。 */
export function forkLineage(s: ForkFields | null | undefined): ForkLineage | null {
  const parentId = String(s?.parent_id || '').trim()
  if (!parentId) return null
  return {
    parentId,
    parentTitle: String(s?.parent_title || '').trim() || '未命名对话',
    gone: Boolean(s?.parent_gone),
  }
}

function clip(text: string, max: number): string {
  const chars = Array.from(text)
  return chars.length > max ? chars.slice(0, max).join('') + '…' : text
}

/** 来处的一句话：侧栏悬浮提示与聊天顶部提示共用。 */
export function lineageText(l: ForkLineage): string {
  return `分叉自「${clip(l.parentTitle, 24)}」${l.gone ? '（原对话已删除）' : ''}`
}

/** 顶栏标签里的短版：标题截短，已删除只留三个字；全文放在悬浮提示（lineageText）里。 */
export function lineageChip(l: ForkLineage): string {
  return `分叉自「${clip(l.parentTitle, 12)}」${l.gone ? '（已删除）' : ''}`
}

export type NoticeTone = 'success' | 'info' | 'warning' | 'error'

export interface ForkNotice {
  tone: NoticeTone
  text: string
}

/** 一次分叉的结果 → 给用户看的话。失败与"暂时不能分叉"分开：后者不是故障。 */
export function forkNotice(r: ForkReply | null | undefined, fallbackTitle = ''): ForkNotice {
  if (!r || !r.success) {
    const why = String(r?.error || '').trim() || '分叉失败'
    return { tone: r?.busy ? 'info' : 'error', text: why }
  }
  const from = clip(String(r.parent_title || fallbackTitle || '原对话').trim() || '原对话', 24)
  const warn = (r.warnings || []).map(w => String(w).trim()).filter(Boolean)
  const text = `已从「${from}」分叉；两边互不影响，但仍共用同一个工作区`
  // 对话已经分叉成功，只是附带的东西（计划任务清单）没复制全：用警告色，停留更久，让人读到
  return warn.length ? { tone: 'warning', text: `${text}。${warn.join('；')}` } : { tone: 'success', text }
}

export interface ForkContext {
  sessionId: string
  sending: boolean
  /** 停在等批准或回答。 */
  awaiting: boolean
  forking: boolean
  hasUserMessage: boolean
}

/**
 * 现在能不能分叉；不能就说为什么（空串 = 能）。
 * 后端还会核对一次（也看得到别的进程里的运行），这里只是让提示更早、少走一趟。
 */
export function forkBlocker(c: ForkContext): string {
  if (c.forking) return '正在分叉，请稍候'
  if (!c.sessionId) return '还没有对话，发一条消息后再分叉'
  if (c.sending) return '这条对话正在运行。等它结束，或先点停止，再分叉。'
  if (c.awaiting) return '这条对话停在等你批准或回答。先处理完它，再分叉。'
  if (!c.hasUserMessage) return '这条对话还是空的，没什么可分叉的'
  return ''
}

/** 新对话的 id；回答里没有就是服务端出了问题。 */
export function forkedId(r: ForkReply | null | undefined): string {
  return String(r?.session?.id || r?.id || '').trim()
}
