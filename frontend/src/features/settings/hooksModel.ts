// 设置 / 用户钩子的纯展示逻辑：状态一句话怎么说、事件和结果叫什么、时间怎么读。不依赖 Vue，方便单测。
import type { HookInfo, HooksStatus } from '../../shared/ipc/types'

/** 事件按它们在一次对话里发生的先后排；名称与 Go 的 userhooks.Events 一致。 */
export const HOOK_EVENTS = [
  { id: 'UserPromptSubmit', label: '收到消息时', hint: '你发出一条消息、模型还没看到它之前：可以拒绝这条消息，或给它补一段上下文。' },
  { id: 'PreToolUse', label: '工具调用前', hint: '工具真正运行之前（需要你确认的工具，先等你同意）：可以拦下这一次调用，模型会看到拦截原因。' },
  { id: 'PostToolUse', label: '工具调用后', hint: '工具运行完之后：只用来记录和审计，不能改结果。' },
  { id: 'Stop', label: '一轮结束后', hint: '智能体答完一轮之后：适合发通知、写审计，不会拖慢界面。' },
] as const

export function eventLabel(event: string): string {
  return HOOK_EVENTS.find(e => e.id === event)?.label ?? event
}

export type Tone = 'ok' | 'warn' | 'bad' | 'dim'

const RESULTS: Record<string, { label: string; tone: Tone }> = {
  ok: { label: '放行', tone: 'ok' },
  blocked: { label: '已拦截', tone: 'warn' },
  failed: { label: '脚本出错', tone: 'bad' },
  timeout: { label: '超时', tone: 'bad' },
  canceled: { label: '已取消', tone: 'dim' },
}

export function resultLabel(result: string): string {
  return RESULTS[result]?.label ?? result
}

export function resultTone(result: string): Tone {
  return RESULTS[result]?.tone ?? 'dim'
}

export interface HooksSummary {
  tone: Tone | 'off'
  text: string
}

/** 卡片顶部那一句话：现在有几个钩子在起作用，没起作用时是为什么。 */
export function hooksSummary(s: HooksStatus): HooksSummary {
  if (!s.enabled) return { tone: 'off', text: s.disabled_reason || '用户钩子已关闭' }
  if (s.error) {
    return s.total > 0
      ? { tone: 'bad', text: `配置文件有错误，暂时继续使用上一份有效配置（${s.total} 个钩子）` }
      : { tone: 'bad', text: '配置文件有错误，目前没有钩子生效' }
  }
  if (!s.exists) return { tone: 'dim', text: '还没有配置：数据目录里没有 hooks.json' }
  if (s.total === 0) return { tone: 'dim', text: s.problems.length > 0 ? '配置文件里没有一个钩子可用' : '配置文件里没有钩子' }
  const base = `已启用 ${s.total} 个钩子`
  return s.problems.length > 0 ? { tone: 'warn', text: `${base}，另有 ${s.problems.length} 处写法有问题（已跳过）` } : { tone: 'ok', text: base }
}

export interface HookGroup {
  id: string
  label: string
  hint: string
  hooks: HookInfo[]
}

/** 按事件分组，没有钩子的事件也保留，让人一眼看出「这个时机我没配」。 */
export function groupHooks(hooks: HookInfo[]): HookGroup[] {
  return HOOK_EVENTS.map(e => ({ id: e.id, label: e.label, hint: e.hint, hooks: hooks.filter(h => h.event === e.id) }))
}

/** 出错或超时时怎么办。 */
export function onFailureLabel(v: string): string {
  return v === 'block' ? '出错时拦截' : '出错时放行'
}

/** 「只对 Bash、Edit 这类工具生效」；没写匹配就是所有工具。收到消息、一轮结束两个事件与工具无关。 */
export function matcherLabel(event: string, matcher?: string): string {
  if (event === 'UserPromptSubmit' || event === 'Stop') return ''
  const m = (matcher ?? '').trim()
  return m === '' || m === '*' ? '所有工具' : m
}

/** 本地时间的 时:分:秒；今天以外的日子带上月/日。无效时间原样给回。 */
export function formatClock(iso: string | undefined, now: Date = new Date()): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const p = (n: number) => String(n).padStart(2, '0')
  const clock = `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
  const today = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate()
  return today ? clock : `${d.getMonth() + 1}/${d.getDate()} ${clock}`
}

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—'
  if (ms < 1000) return `${Math.round(ms)} 毫秒`
  const s = ms / 1000
  return `${s < 10 ? s.toFixed(1).replace(/\.0$/, '') : Math.round(s)} 秒`
}

/** 校验和只用来核对「现在读到的是不是我刚改的那份」，前 8 位够用。 */
export function shortHash(sha?: string): string {
  return sha ? sha.slice(0, 8) : '—'
}

/** 点「复制示例」时放进剪贴板的 hooks.json：拦截 rm -rf 的最小例子。 */
export const HOOKS_EXAMPLE = `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "python D:\\\\tools\\\\guard.py", "timeout": 10, "onFailure": "block" }
        ]
      }
    ],
    "Stop": [
      { "hooks": [ { "type": "command", "command": "python D:\\\\tools\\\\notify.py" } ] }
    ]
  }
}`
