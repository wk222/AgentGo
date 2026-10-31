// ChatPanel 的数据契约：由 AppShell 通过 provide 注入，ChatPanel 用 inject 读取。
// 用 reactive() 包一层，ref 会被自动解包，所以读取处直接写 ctx.messages / ctx.sending，不用 .value。
import { inject, provide, reactive } from 'vue'
import type { Message, Session } from '../../stores/useChat'

export interface ChatContext {
  // 状态
  messages: Message[]
  sessionId: string
  sessions: Session[]
  sessionLoading: boolean
  sending: boolean
  /** 正在分叉当前对话（防连点）。 */
  forking: boolean
  awaitingInteract: boolean
  runStatusLine: string
  chatPaneKey: number
  contextOpen: boolean
  formInputs: Record<string, Record<string, any>>
  modeProfile: string
  modeCanvas: string
  modeSaving: boolean
  // 动作
  send: (text: string, images?: string[]) => void
  stop: () => void
  toggleContext: () => void
  reload: () => void
  newChat: () => void
  clearChat: () => void
  /** 把当前对话整条复制成新对话并切过去；结果（含被拒绝的原因）用提示告诉用户。 */
  forkChat: () => void
  selectSession: (id: string) => void
  deleteSession: (id: string) => void
  approve: (id: string, note?: string, overrideArgs?: string, remember?: '' | 'call' | 'tool') => void
  reject: (id: string, note?: string) => void
  steer: (instruction: string) => Promise<boolean>
  submitAUI: (msg: Message, val: string, idx: number) => void
  submitAUIAction: (msg: Message, val: string, idx: number) => void
  submitQuestion: (msg: Message, answer: string, idx: number) => void
  setFormInput: (key: string, field: string, val: any) => void
  changeMode: (profile: string, canvas: string) => void
}

/** 传入的对象字段可以是 ref/computed（会被解包）。 */
export type ChatContextSource = { [K in keyof ChatContext]: ChatContext[K] | { value: ChatContext[K] } }

const KEY = Symbol('agentgo.chat')

export function provideChatContext(src: ChatContextSource): ChatContext {
  const ctx = reactive(src as any) as ChatContext
  provide(KEY, ctx)
  return ctx
}

export function useChatContext(): ChatContext {
  const ctx = inject<ChatContext | null>(KEY, null)
  if (!ctx) throw new Error('ChatPanel 必须在 provideChatContext 之下使用')
  return ctx
}
