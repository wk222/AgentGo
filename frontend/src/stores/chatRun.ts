// 每个会话各自的"对话 + 运行"状态。
//
// 以前 useChat 只有一份全局的 messages / sending / awaitingInteract / streamMsgIndex，
// 切换会话时只能把运行取消掉再重置，回来后正在跑的工具、等待批准、流式文本全丢了。
// 现在每个会话一份 SessionRun，切换只是把"当前会话"指针换掉：后台会话照常接收事件、
// 照常累积消息，切回来时原样恢复。
import { reactive } from 'vue'
import type { RuntimeEvent } from '../shared/runtime-client'
import type { Message } from './useChat'

export interface SessionRun {
  /** 会话 id；还没建会话（首条消息发送前）时为空。 */
  sid: string
  msgs: Message[]
  /** 已经从服务端加载过历史（或是本页新建的会话）。 */
  loaded: boolean
  /** 一次运行正在进行（流式输出 / 工具执行 / 续跑）。 */
  sending: boolean
  /** 运行停在审批、提问或交互卡片上，等用户处理。 */
  awaiting: boolean
  status: string
  /** 正在流式输出的文本消息下标，没有为 -1。 */
  streamIdx: number
  /** 当前正在累积的"思考"消息下标，没有为 -1。 */
  reasoningIdx: number
  runID: string
  /** 本轮（或续跑段）文本开始的消息下标，用来判断 done 时是否还要补服务端文本。 */
  segStart: number
  /** RunEngine 已发出但 run_id 还没回来：这段时间的事件先缓存。 */
  starting: boolean
  startEvents: RuntimeEvent[]
  ignoredRunID: string
  /** 每次发送/停止自增，让过期的异步回调认出自己已经被取代。 */
  sendSeq: number
  /** 正在等待的 A2UI 交互 id。 */
  interactId: string
}

export function createRun(sid: string): SessionRun {
  return reactive({
    sid,
    msgs: [],
    loaded: false,
    sending: false,
    awaiting: false,
    status: '',
    streamIdx: -1,
    reasoningIdx: -1,
    runID: '',
    segStart: 0,
    starting: false,
    startEvents: [],
    ignoredRunID: '',
    sendSeq: 0,
    interactId: '',
  }) as SessionRun
}

/** "新会话已就绪" / "加载会话…" 这类占位消息：不是真实对话内容。 */
export function isPlaceholderMessage(m: Message): boolean {
  const c = String(m?.content || '')
  return m?.role === 'assistant' && (c.includes('就绪') || c.includes('加载会话'))
}

export type RunBadge = 'running' | 'awaiting'

/** 会话列表上的运行标记：正在跑，或停在等用户处理。 */
export function runBadge(r: Pick<SessionRun, 'sending' | 'awaiting' | 'starting'>): RunBadge | '' {
  if (r.awaiting) return 'awaiting'
  if (r.sending || r.starting) return 'running'
  return ''
}

/**
 * 服务端历史与内存里的消息是不是"同一段对话"。
 * 只比文字和审批数量：内存里多出来的工具气泡 / 思考块不算差异，
 * 文字被分成几条消息也不算（服务端可能把工具前后的文字合并存），
 * 这样切回已有会话时不会用服务端的精简版把它们冲掉。
 */
export function sameConversation(a: Message[], b: Message[]): boolean {
  const sig = (list: Message[]) => {
    const text = list
      .filter(m => m.type === 'text' && (m.role === 'user' || m.role === 'assistant') && !isPlaceholderMessage(m) && (m.content || '').trim())
      .map(m => `${m.role === 'user' ? 'U' : 'A'}${m.content || ''}`)
      .join('')
      .replace(/\s+/g, '')
    const pending = list.filter(m => (m.type === 'approval' || m.type === 'question') && !m.resolved).length
    return `${text}#${pending}`
  }
  return sig(a) === sig(b)
}
