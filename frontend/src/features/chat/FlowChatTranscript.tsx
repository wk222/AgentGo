import { defineComponent, ref, computed, PropType } from 'vue'
import type { Message } from '../../stores/useChat'
import { isRollbackNote, noteHeadline } from '../../stores/turnCheckpointModel'
import { wailsCall } from '../../shared/wails'
import { notify, confirmDialog } from '../../shared/ui/notify'
import { useChatContext } from './chatContext'
import { useTurnRollback } from './useTurnRollback'
import ReasoningBubble from './ReasoningBubble'
import ToolCallBubble from './ToolCallBubble'
import AUICard from './AUICard'
import { userDisplayText } from './userText'
import EvidenceAnswer from './EvidenceAnswer'

export function extractThinking(content: string): { thinking: string; main: string } {
  if (!content) return { thinking: '', main: '' }
  const match = content.match(/<think>([\s\S]*?)<\/think>/i)
  if (match) {
    return {
      thinking: match[1].trim(),
      main: content.replace(/<think>[\s\S]*?<\/think>/i, '').trim(),
    }
  }
  const openMatch = content.match(/<think>([\s\S]*)$/i)
  if (openMatch) {
    return {
      thinking: openMatch[1].trim(),
      main: content.replace(/<think>[\s\S]*$/i, '').trim(),
    }
  }
  return { thinking: '', main: content }
}

/**
 * 从 AI 文本回复末尾提取可点击的交互选项（如 A/B/C 或 1/2/3 方案选项），
 * 解决 AI 未调用 ask_user 工具而直接在正文中列出选项导致无法点击的问题。
 */
export function extractInlineChoices(text: string): Array<{ label: string; value: string }> {
  if (!text || text.length < 10) return []
  const withoutCode = text.replace(/```[\s\S]*?```/g, '').trim()
  if (!withoutCode) return []
  const tail = withoutCode.slice(-900)
  const hasPromptCue = /[?？]|请选择|请确认|你希望|您希望|倾向于|哪个方案|哪种方式|下一步|如何处理|选项|方案\s*[123一二三AB]/i.test(tail)
  if (!hasPromptCue) return []

  const lines = tail.split(/\r?\n/).map(l => l.trim()).filter(Boolean)
  const items: Array<{ label: string; value: string }> = []
  const optRe = /^(?:[-*•]\s*)?(?:\*\*|`)?([A-D]|[1-6]|方案[一二三四1-4]|选项[一二三四1-4])(?:[.)、：:]|\*\*|`|\s)+(.{2,72})$/i

  for (const line of lines) {
    const m = line.match(optRe)
    if (m) {
      const prefix = m[1].trim()
      const body = m[2].replace(/\*\*/g, '').replace(/`/g, '').trim()
      if (!body || body.startsWith('http')) continue
      const shortBody = body.length > 34 ? body.slice(0, 34) + '…' : body
      const label = `${prefix}. ${shortBody}`.replace(/^(方案.|选项.)\.\s*/, '$1：')
      items.push({ label, value: `${prefix}: ${body}` })
    }
  }
  if (items.length >= 2 && items.length <= 6) {
    return items
  }
  return []
}

/**
 * 活动分类：
 * Classify low-risk observation / exploration tool calls so consecutive read-only
 * probes can be folded into a single Explore Capsule, while edits/commands/errors stay prominent.
 */
const EXPLORE_TOOL_PATTERN = /^(read|list|search|grep|glob|get|find|inspect|stat|view|query|fetch|ls|cat|head|tail|git_status|git_diff|git_log|memory_search|skill)/i
const MUTATING_TOOL_PATTERN = /(write|edit|replace|delete|remove|create|exec|bash|shell|run|build|deploy|commit|push|patch|install)/i
const SUBAGENT_TOOL_PATTERN = /(subagent|delegate|spawn_agent|run_subtask|background_task|bgtask|dispatch_agent|task_agent|parallel_task)/i

function parseSubagentInfo(toolName: string, rawArgs?: string): {
  agentRole: string
  taskPrompt: string
  isParallel: boolean
} | null {
  if (!SUBAGENT_TOOL_PATTERN.test(toolName || '')) return null
  let agentRole = 'Subagent'
  let taskPrompt = ''
  const isParallel = /parallel|bgtask|background/i.test(toolName || '')
  if (rawArgs) {
    try {
      const p = JSON.parse(rawArgs)
      if (p && typeof p === 'object') {
        agentRole = String(
          p.subagent_type || p.agent || p.role || p.name || p.worker || (isParallel ? 'Parallel Worker' : 'Subagent'),
        ).trim()
        taskPrompt = String(
          p.prompt || p.task || p.instruction || p.description || p.query || p.goal || '',
        ).trim()
      }
    } catch {
      taskPrompt = rawArgs.slice(0, 120)
    }
  }
  return {
    agentRole: agentRole || 'Subagent',
    taskPrompt: taskPrompt || `执行子智能体任务 (${toolName})`,
    isParallel,
  }
}

export function isExploreToolMessage(msg: Message): boolean {
  if (msg.type !== 'tool_call') return false
  if (msg.status === 'error') return false
  const name = String(msg.toolName || '').trim()
  if (!name) return false
  if (MUTATING_TOOL_PATTERN.test(name)) return false
  return EXPLORE_TOOL_PATTERN.test(name)
}

export interface TranscriptSegment {
  kind: 'single' | 'explore_group'
  messages: Array<{ msg: Message; index: number }>
  turnIndex: number
  key: string
}

export interface TurnItem {
  turnNumber: number
  msgIndex: number
  key: string
  summary: string
  hasTools: boolean
  hasError: boolean
  streaming: boolean
}

export function buildTranscriptAndTurns(messages: Message[]): {
  segments: TranscriptSegment[]
  turns: TurnItem[]
} {
  const segments: TranscriptSegment[] = []
  const turns: TurnItem[] = []
  let currentTurn = 0
  let i = 0

  while (i < messages.length) {
    const msg = messages[i]
    if (msg.role === 'user' && (!msg.type || msg.type === 'text')) {
      currentTurn++
      const rawText = userDisplayText(msg.content).trim().replace(/\s+/g, ' ')
      turns.push({
        turnNumber: currentTurn,
        msgIndex: i,
        key: String(msg._key || `turn-${currentTurn}-${i}`),
        summary: rawText.slice(0, 42) + (rawText.length > 42 ? '…' : '') || `第 ${currentTurn} 轮对话`,
        hasTools: false,
        hasError: false,
        streaming: false,
      })
    } else if (turns.length > 0) {
      const activeTurn = turns[turns.length - 1]
      if (msg.type === 'tool_call') activeTurn.hasTools = true
      if (msg.status === 'error') activeTurn.hasError = true
      if (msg.streaming) activeTurn.streaming = true
    }

    // Group 2+ consecutive read-only explore tool calls into an ExploreCapsule
    if (isExploreToolMessage(msg)) {
      const group: Array<{ msg: Message; index: number }> = [{ msg, index: i }]
      let j = i + 1
      while (j < messages.length && isExploreToolMessage(messages[j])) {
        group.push({ msg: messages[j], index: j })
        if (turns.length > 0) {
          turns[turns.length - 1].hasTools = true
        }
        j++
      }
      if (group.length >= 2) {
        segments.push({
          kind: 'explore_group',
          messages: group,
          turnIndex: currentTurn,
          key: `explore-${group[0].msg._key || group[0].index}-${group.length}`,
        })
        i = j
        continue
      }
    }

    segments.push({
      kind: 'single',
      messages: [{ msg, index: i }],
      turnIndex: currentTurn,
      key: String(msg._key || `msg-${i}`),
    })
    i++
  }

  return { segments, turns }
}

function summarizeExploreGroup(items: Array<{ msg: Message; index: number }>): {
  title: string
  runningCount: number
  toolBadges: string[]
} {
  let readCount = 0
  let searchCount = 0
  let listCount = 0
  let otherCount = 0
  let runningCount = 0
  const badgeSet = new Set<string>()

  for (const { msg } of items) {
    const name = String(msg.toolName || 'tool')
    badgeSet.add(name)
    if (msg.status === 'running') runningCount++
    if (/read|cat|view|get|fetch/i.test(name)) readCount++
    else if (/search|grep|glob|find|query/i.test(name)) searchCount++
    else if (/list|ls|stat/i.test(name)) listCount++
    else otherCount++
  }

  const parts: string[] = []
  if (readCount > 0) parts.push(`读取 ${readCount} 项`)
  if (searchCount > 0) parts.push(`检索 ${searchCount} 次`)
  if (listCount > 0) parts.push(`列目录 ${listCount} 次`)
  if (otherCount > 0) parts.push(`探测 ${otherCount} 次`)

  const detail = parts.length > 0 ? `（${parts.join(' · ')}）` : ''
  return {
    title: `已连续探索 ${items.length} 步上下文${detail}`,
    runningCount,
    toolBadges: Array.from(badgeSet).slice(0, 4),
  }
}

export default defineComponent({
  name: 'FlowChatTranscript',
  props: {
    messages: { type: Array as PropType<Message[]>, required: true },
    formInputs: { type: Object as PropType<Record<string, Record<string, any>>>, required: true },
    onApprove: { type: Function as PropType<(id: string, note?: string, overrideArgs?: string, remember?: '' | 'call' | 'tool') => void>, required: true },
    onReject: { type: Function as PropType<(id: string, note?: string) => void>, required: true },
    onSubmitAUI: { type: Function as PropType<(msg: Message, val: string, idx: number) => void>, required: true },
    onSubmitAUIAction: { type: Function as PropType<(msg: Message, val: string, idx: number) => void>, required: true },
    onSubmitQuestion: { type: Function as PropType<(msg: Message, answer: string, idx: number) => void>, required: true },
    onFormInput: { type: Function as PropType<(key: string, field: string, val: any) => void>, required: true },
    onJumpTurn: { type: Function as PropType<(msgIndex: number) => void>, required: true },
    onScrollEdge: { type: Function as PropType<(edge: 'top' | 'bottom') => void>, required: true },
  },
  setup(props) {
    // 必须用 AppShell 注入的那一份：useChat() 每调用一次就是一份全新的空状态（sessionId 恒为空），
    // 之前这里自己调 useChat()，「撤销未接受改动」拿到空会话 id、什么也不做。
    const chat = useChatContext()
    const rollback = useTurnRollback(chat)
    const expandedExploreKeys = ref<Record<string, boolean>>({})
    const expandedSubagentKeys = ref<Record<string, boolean>>({})
    const questionInput = ref<Record<string, string>>({})
    const questionChoices = ref<Record<string, string[]>>({})
    const approvalEditOpen = ref<Record<string, boolean>>({})
    const approvalNote = ref<Record<string, string>>({})
    const approvalArgs = ref<Record<string, string>>({})
    const activeTurnIdx = ref<number>(0)

    // 审阅基线是会话级的，不是按轮的：从开始跟踪 AI 改动起算，只在用户接受或撤销时才推进。
    // 所以无论点的是哪条消息上的按钮，撤销的都是本会话全部未接受的改动，文案必须如实说明。
    const discardUnacceptedChanges = async (turnIdx: number, userPrompt: string) => {
      const ok = await confirmDialog(
        `这会撤销本会话里所有尚未接受的文件改动：改过的文件恢复原样，新建的文件会被删除。\n\n范围是整个会话，不只是第 ${turnIdx} 轮；这期间你手动做的修改也会一起撤销。已经接受的改动不受影响。\n\n同时会把这一轮提问复制到剪贴板，方便重新发起。`,
        { title: '撤销未接受的文件改动', okText: '确认撤销', danger: true },
      )
      if (!ok) return
      try {
        if (userPrompt) {
          await navigator.clipboard?.writeText(userPrompt).catch(() => {})
        }
        const sid = chat.sessionId
        if (sid) {
          const res = await wailsCall('RejectAllWorkspaceReview', sid)
          if (res?.success === false && res?.error) {
            const reason = String(res.error)
            notify.warning(reason.includes('no active AI review') ? '当前没有可撤销的文件改动' : `没有完成撤销：${reason}`)
          } else {
            notify.success('已撤销未接受的文件改动，这一轮提问已复制到剪贴板')
          }
        }
      } catch (e: any) {
        notify.error(`撤销文件改动失败：${e?.message || e}`)
      }
    }

    const toggleApprovalEdit = (aid: string, rawArgs?: string) => {
      const next = !approvalEditOpen.value[aid]
      approvalEditOpen.value[aid] = next
      if (next && approvalArgs.value[aid] === undefined && rawArgs) {
        try {
          approvalArgs.value[aid] = JSON.stringify(JSON.parse(rawArgs), null, 2)
        } catch {
          approvalArgs.value[aid] = rawArgs
        }
      }
    }

    const toggleExploreGroup = (key: string) => {
      expandedExploreKeys.value[key] = !expandedExploreKeys.value[key]
    }

    const toggleQuestionChoice = (key: string, value: string) => {
      const list = questionChoices.value[key] || []
      questionChoices.value[key] = list.includes(value)
        ? list.filter(x => x !== value)
        : [...list, value]
    }

    const parsed = computed(() => buildTranscriptAndTurns(props.messages))

    return () => {
      const { segments, turns } = parsed.value

      return (
        <div class="flowchat-transcript-wrap">
          <div class="flowchat-transcript-main">
            {segments.map((seg) => {
              if (seg.kind === 'explore_group') {
                const isExpanded = !!expandedExploreKeys.value[seg.key]
                const summary = summarizeExploreGroup(seg.messages)
                return (
                  <div key={seg.key} class="msg-row assistant tool-call-row">
                    <div class={['flowchat-explore-capsule', isExpanded && 'expanded', summary.runningCount > 0 && 'running']}>
                      <button
                        type="button"
                        class="flowchat-explore-header"
                        onClick={() => toggleExploreGroup(seg.key)}
                        title="点击展开或折叠连续只读探索步骤"
                      >
                        <div class="flowchat-explore-left">
                          <span class={['flowchat-explore-dot', summary.runningCount > 0 && 'pulsing']}></span>
                          <span class="flowchat-explore-title">{summary.title}</span>
                          <div class="flowchat-explore-badges">
                            {summary.toolBadges.map(b => (
                              <span key={b} class="flowchat-explore-badge">{b}</span>
                            ))}
                          </div>
                        </div>
                        <span class="flowchat-explore-toggle">
                          {isExpanded ? '收起 ▴' : '展开详情 ▾'}
                        </span>
                      </button>
                      {isExpanded && (
                        <div class="flowchat-explore-body">
                          {seg.messages.map(({ msg, index }) => (
                            <ToolCallBubble
                              key={msg._key || index}
                              toolName={msg.toolName || 'tool'}
                              arguments={msg.arguments}
                              content={msg.content}
                              status={msg.status}
                            />
                          ))}
                        </div>
                      )}
                    </div>
                  </div>
                )
              }

              const { msg, index } = seg.messages[0]
              const isUserTurnStart = msg.role === 'user' && !msg.type

              // 模型的思考过程（reasoning 事件累积而成）：折叠显示，点开可看全文。
              if (msg.type === 'reasoning') {
                return (
                  <div key={seg.key} class="msg-row assistant tool-call-row" data-msg-idx={index}>
                    <ReasoningBubble thinking={msg.content || ''} streaming={!!msg.streaming} />
                  </div>
                )
              }

              if (msg.type === 'aui') {
                return (
                  <div key={seg.key} class="msg-row assistant" data-msg-idx={index}>
                    <AUICard
                      msg={msg}
                      index={index}
                      formInputs={props.formInputs}
                      onSubmitAUI={props.onSubmitAUI}
                      onSubmitAUIAction={props.onSubmitAUIAction}
                      onFormInput={props.onFormInput}
                    />
                  </div>
                )
              }

              if (msg.type === 'approval') {
                const aid = msg.approval_id || String(index)
                const editOpen = !!approvalEditOpen.value[aid]
                const noteVal = approvalNote.value[aid] || ''
                const argsVal = approvalArgs.value[aid]
                // 逆向档里“要不要开始越狱”：不是被暂停的工具调用，没有可改的参数，
                // 也没有“本会话总是允许”这一说，按钮只回答一个问题。
                const isArmor = msg.toolName === 'armor_break'

                // 已处理的审批折叠为单行紧凑胶囊，不再保留大块黄框与禁用按钮
                if (msg.resolved) {
                  return (
                    <div key={seg.key} class="msg-row assistant tool-call-row" data-msg-idx={index}>
                      <div class="flowchat-approval-resolved">
                        <span class="flowchat-approval-resolved-icon">✓</span>
                        <span class="flowchat-approval-resolved-label">{isArmor ? '已回答' : '已确认执行'}</span>
                        {msg.toolName && <code class="flowchat-approval-tool">{msg.toolName}</code>}
                        <span class="flowchat-approval-resolved-desc">{msg.content || '命令审批已完成'}</span>
                      </div>
                    </div>
                  )
                }

                return (
                  <div key={seg.key} class="msg-row assistant" data-msg-idx={index}>
                    <div class="msg-approval">
                      <div class="msg-approval-head">
                        <div class="msg-approval-label">
                          <span class="msg-approval-dot" />
                          <span>{isArmor ? '是否开始越狱' : '等待确认执行'}</span>
                        </div>
                        {msg.toolName && !isArmor && (
                          <code class="flowchat-approval-tool">{msg.toolName}</code>
                        )}
                      </div>
                      <div class="msg-approval-prompt">{msg.content || '需要您的批准才能继续'}</div>
                      {!isArmor && !editOpen && msg.arguments && (
                        <pre class="msg-approval-args-preview">{msg.arguments.length > 220 ? msg.arguments.slice(0, 220) + '…' : msg.arguments}</pre>
                      )}
                      {editOpen && (
                        <div class="msg-approval-edit-box">
                          {msg.arguments !== undefined && (
                            <div>
                              <div class="msg-approval-edit-label">工具调用参数（可修改后放行 overrideArgs）：</div>
                              <textarea
                                rows={3}
                                class="msg-approval-textarea"
                                value={argsVal ?? (msg.arguments || '')}
                                onInput={(e: Event) => { approvalArgs.value[aid] = (e.target as HTMLTextAreaElement).value }}
                              />
                            </div>
                          )}
                          <input
                            class="msg-approval-note-input"
                            placeholder="附言或拒绝理由（可选，拒绝时 Agent 会根据此反馈调整方案）…"
                            value={noteVal}
                            onInput={(e: Event) => { approvalNote.value[aid] = (e.target as HTMLInputElement).value }}
                          />
                        </div>
                      )}
                      <div class="msg-approval-actions">
                        <button
                          type="button"
                          class="msg-approve-btn ok"
                          onMousedown={(e: MouseEvent) => e.preventDefault()}
                          onClick={() => {
                            const override = argsVal !== undefined && argsVal !== msg.arguments ? argsVal.trim() : ''
                            props.onApprove(msg.approval_id || '', noteVal.trim(), override)
                          }}
                        >
                          {isArmor ? '⚡ 开始越狱' : argsVal !== undefined && argsVal !== msg.arguments ? '✓ 改参并批准' : '✓ 批准执行'}
                        </button>
                        {!isArmor && (
                          <button
                            type="button"
                            class="msg-approve-btn ghost"
                            title="本会话内不再询问；关闭会话或重启后失效"
                            onMousedown={(e: MouseEvent) => e.preventDefault()}
                            onClick={() => props.onApprove(msg.approval_id || '', noteVal.trim(), '', ['execute_bash', 'run_elevated', 'computer_use'].includes(msg.toolName || '') ? 'call' : 'tool')}
                          >
                            {['execute_bash', 'run_elevated', 'computer_use'].includes(msg.toolName || '') ? '✓ 批准并记住本次' : '✓ 本会话总是允许'}
                          </button>
                        )}
                        <button
                          type="button"
                          class="msg-approve-btn no"
                          onMousedown={(e: MouseEvent) => e.preventDefault()}
                          onClick={() => props.onReject(msg.approval_id || '', noteVal.trim())}
                        >
                          {isArmor ? '✕ 不用' : noteVal.trim() ? '✕ 带意见拒绝' : '✕ 拒绝'}
                        </button>
                        {!isArmor && (
                          <button
                            type="button"
                            class="msg-approve-btn ghost"
                            onMousedown={(e: MouseEvent) => e.preventDefault()}
                            onClick={() => toggleApprovalEdit(aid, msg.arguments)}
                          >
                            {editOpen ? '收起改参' : '✎ 改参 / 附言'}
                          </button>
                        )}
                      </div>
                    </div>
                  </div>
                )
              }

              if (msg.type === 'question') {
                const key = msg.interactId || msg.approval_id || String(index)
                let qData: Record<string, any> = (msg.data && typeof msg.data === 'object') ? { ...msg.data } : {}
                if (msg.arguments && typeof msg.arguments === 'string' && msg.arguments.trim()) {
                  try {
                    const parsed = JSON.parse(msg.arguments)
                    if (parsed && typeof parsed === 'object') qData = { ...parsed, ...qData }
                  } catch {}
                }
                let rawChoices: any = qData.choices ?? qData.options ?? []
                if (typeof rawChoices === 'string' && rawChoices.trim()) {
                  try {
                    const arr = JSON.parse(rawChoices)
                    if (Array.isArray(arr)) rawChoices = arr
                  } catch {
                    rawChoices = rawChoices.split(/[,，\n]/).map((s: string) => s.trim()).filter(Boolean)
                  }
                }
                const choices: any[] = Array.isArray(rawChoices) ? rawChoices : []
                const multiple = !!qData.multiple
                const freeText = qData.free_text !== false && qData.freeText !== false
                const selected = questionChoices.value[key] || []
                const promptText = msg.content || qData.prompt || qData.question || '请选择或输入您的回答：'
                return (
                  <div key={seg.key} class="msg-row assistant" data-msg-idx={index}>
                    <div class={['msg-approval', msg.resolved && 'resolved']} style={{ borderLeftColor: 'var(--info)' }}>
                      <div class="msg-approval-label" style={{ color: 'var(--info)' }}>
                        {msg.resolved ? '已回答（点击选项可再次发送）' : '需要您的选择 / 输入'}
                      </div>
                      <div class="msg-approval-prompt">{promptText}</div>
                      {choices.length > 0 && (
                        <div class="msg-choice-list">
                          {choices.map((c: any, i: number) => {
                            const isPrimitive = typeof c === 'string' || typeof c === 'number'
                            const val = isPrimitive
                              ? String(c).trim()
                              : String(c?.value ?? c?.id ?? c?.label ?? c?.title ?? c?.name ?? i)
                            const label = isPrimitive
                              ? String(c).trim()
                              : String(c?.label ?? c?.title ?? c?.name ?? val)
                            return multiple ? (
                              <label key={`${val}_${i}`} class="msg-choice-check" style={{ cursor: 'pointer' }}>
                                <input
                                  type="checkbox"
                                  checked={selected.includes(val)}
                                  onChange={() => toggleQuestionChoice(key, val)}
                                />
                                <span>{label}</span>
                              </label>
                            ) : (
                              <button
                                key={`${val}_${i}`}
                                type="button"
                                class="msg-choice-btn"
                                style={{ cursor: 'pointer' }}
                                onMousedown={(e: MouseEvent) => e.preventDefault()}
                                onClick={() => props.onSubmitQuestion(msg, val, index)}
                              >
                                {label}
                              </button>
                            )
                          })}
                        </div>
                      )}
                      <div style={{ display: 'flex', gap: '8px' }}>
                        {freeText && (
                          <input
                            style={{ flex: 1, padding: '6px 10px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)', fontSize: '13px' }}
                            placeholder="输入回答或补充说明…"
                            value={questionInput.value[key] || ''}
                            onInput={(e: Event) => questionInput.value[key] = (e.target as HTMLInputElement).value}
                            onKeydown={(e: KeyboardEvent) => {
                              if (e.key === 'Enter') {
                                e.preventDefault()
                                const ans = (questionInput.value[key] || '').trim()
                                if (ans) props.onSubmitQuestion(msg, ans, index)
                              }
                            }}
                          />
                        )}
                        <button
                          type="button"
                          class="msg-approve-btn ok"
                          onMousedown={(e: MouseEvent) => e.preventDefault()}
                          onClick={() => {
                            const answer = multiple ? selected.join(', ') : (questionInput.value[key] || selected[0] || '')
                            if (String(answer).trim()) {
                              props.onSubmitQuestion(msg, String(answer).trim(), index)
                            }
                          }}
                        >
                          {msg.resolved ? '重新发送' : '发送'}
                        </button>
                      </div>
                    </div>
                  </div>
                )
              }

              if (msg.type === 'tool_call') {
                const subInfo = parseSubagentInfo(msg.toolName || '', msg.arguments)
                if (subInfo) {
                  const isRunning = msg.status === 'running' || msg.status === 'in_progress'
                  const isFailed = msg.status === 'error' || msg.status === 'failed'
                  const subExpanded = !!expandedSubagentKeys.value[seg.key]
                  return (
                    <div key={seg.key} class="msg-row assistant tool-call-row" data-msg-idx={index}>
                      <div class={['flowchat-subagent-card', isRunning && 'running', isFailed && 'failed']}>
                        <div
                          class="flowchat-subagent-head"
                          onClick={() => { expandedSubagentKeys.value[seg.key] = !subExpanded }}
                        >
                          <div class="flowchat-subagent-title-row">
                            <span class={['flowchat-subagent-dot', isRunning && 'pulsing', isFailed && 'error']} />
                            <span class="flowchat-subagent-badge">
                              {subInfo.isParallel ? '⚡ 并行子任务' : '🤖 子智能体'} · {subInfo.agentRole}
                            </span>
                            <span class="flowchat-subagent-task">{subInfo.taskPrompt}</span>
                          </div>
                          <span class="flowchat-subagent-status">
                            {isRunning ? '执行中…' : isFailed ? '失败' : '已完成'} {subExpanded ? '▴' : '▾'}
                          </span>
                        </div>
                        {isRunning && <div class="flowchat-subagent-progress-bar" />}
                        {subExpanded && (
                          <div class="flowchat-subagent-body">
                            <ToolCallBubble
                              toolName={msg.toolName || 'subagent'}
                              arguments={msg.arguments}
                              content={msg.content}
                              status={msg.status}
                            />
                          </div>
                        )}
                      </div>
                    </div>
                  )
                }

                return (
                  <div key={seg.key} class="msg-row assistant tool-call-row" data-msg-idx={index}>
                    <ToolCallBubble
                      toolName={msg.toolName || 'tool'}
                      arguments={msg.arguments}
                      content={msg.content}
                      status={msg.status}
                      sessionId={chat.sessionId}
                    />
                  </div>
                )
              }

              // 回退之后写进对话的说明行：整段话是给模型读的，放在悬浮提示里，界面上只画一句分隔。
              if (isRollbackNote(msg)) {
                return (
                  <div key={seg.key} class="msg-row assistant tool-call-row" data-msg-idx={index}>
                    <div class="flowchat-rollback-note" title={msg.content}>
                      <span class="flowchat-rollback-note-icon">↩</span>
                      <span>{noteHeadline(msg.content)}</span>
                    </div>
                  </div>
                )
              }

              const { thinking, main } = extractThinking(msg.content || '')
              // 发送时预占的回复气泡：模型直接去调工具 / 被停止时一个字也没有，不留一块空白。
              if (msg.role === 'assistant' && !msg.streaming && !main && !thinking) return null
              const inlineChoices = (!msg.streaming && msg.role === 'assistant') ? extractInlineChoices(main) : []
              return (
                <div
                  key={seg.key}
                  class={['msg-row', msg.role]}
                  data-msg-idx={index}
                  data-turn-idx={isUserTurnStart ? seg.turnIndex : undefined}
                >
                  {msg.role === 'user' ? (
                    <div class="msg-bubble user-bubble">
                      {userDisplayText(msg.content)}
                      {msg.meta?.images && msg.meta.images.length > 0 && (
                        <div class="msg-images" style={{ display: 'flex', gap: '8px', marginTop: msg.content ? '8px' : '0', flexWrap: 'wrap' }}>
                          {msg.meta.images.map((imgUrl: string) => (
                            <img
                              key={imgUrl}
                              src={imgUrl}
                              style={{ maxWidth: '240px', maxHeight: '180px', borderRadius: 'var(--radius-md)', cursor: 'pointer', border: '1px solid var(--border)' }}
                              onClick={() => window.open(imgUrl, '_blank')}
                            />
                          ))}
                        </div>
                      )}
                    </div>
                  ) : (
                    <div class="msg-bubble assistant-bubble">
                      {thinking && (
                        <ReasoningBubble thinking={thinking} streaming={msg.streaming} />
                      )}
                      {(main || msg.streaming) && (
                        <div class="msg-main-content">
                          {!main && msg.streaming && <span class="message-pending"><i /><i /><i /><span>正在处理</span></span>}
                          <EvidenceAnswer messages={props.messages} index={index} html={msg.html && !thinking ? msg.html : undefined} text={main} streaming={msg.streaming} />
                        </div>
                      )}
                      {inlineChoices.length > 0 && (
                        <div class="msg-choice-list" style={{ marginTop: '10px', paddingTop: '8px', borderTop: '1px dashed var(--border-subtle)' }}>
                          {inlineChoices.map((opt, oi) => (
                            <button
                              key={`${opt.value}_${oi}`}
                              type="button"
                              class="msg-choice-btn"
                              style={{ cursor: 'pointer' }}
                              title={`点击发送选择：${opt.value}`}
                              onMousedown={(e: MouseEvent) => e.preventDefault()}
                              onClick={() => props.onSubmitQuestion(msg, opt.value, index)}
                            >
                              {opt.label}
                            </button>
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                  {msg.role === 'user' && msg.content && (
                    <div class="msg-user-actions">
                      <button
                        type="button"
                        class="msg-copy"
                        title="复制此轮提问"
                        onClick={() => navigator.clipboard?.writeText(userDisplayText(msg.content)).catch(() => {})}
                      >
                        复制
                      </button>
                      {rollback.can(msg) && (
                        <button
                          type="button"
                          class="msg-copy msg-checkpoint-btn"
                          disabled={rollback.busy.value}
                          title="把工作区文件恢复到发送这条消息之前的样子；先预览再确认，回退前会自动保存现状，可以撤销"
                          onClick={() => void rollback.rollbackTo(msg)}
                        >
                          ↩ 回到这一轮之前
                        </button>
                      )}
                      <button
                        type="button"
                        class="msg-copy msg-checkpoint-btn"
                        title="撤销本会话里尚未接受的全部文件改动（范围是整个会话，不只是这一轮）"
                        onClick={() => void discardUnacceptedChanges(seg.turnIndex || 1, userDisplayText(msg.content))}
                      >
                        ↶ 撤销未接受改动
                      </button>
                    </div>
                  )}
                </div>
              )
            })}
            {rollback.undoBar.value && (
              <div class="flowchat-undo-bar" role="status">
                <span class="flowchat-undo-text">{rollback.undoBar.value.text}</span>
                <button
                  type="button"
                  class="msg-copy"
                  disabled={rollback.busy.value || chat.sending}
                  title="把文件恢复成回退之前的样子"
                  onClick={() => void rollback.undo()}
                >
                  撤销这次回退
                </button>
                <button type="button" class="msg-copy" title="关闭" onClick={rollback.dismissUndo}>×</button>
              </div>
            )}
          </div>

          {/* 轮次导轨：多轮会话的右侧快速导航 */}
          {turns.length >= 2 && (
            <aside class="flowchat-turn-rail" aria-label="会话回合快速导航">
              <button
                type="button"
                class="flowchat-rail-edge-btn"
                title="回到顶部"
                onClick={() => props.onScrollEdge('top')}
              >
                ↑
              </button>
              <div class="flowchat-turn-rail-list">
                {turns.map((t) => (
                  <button
                    key={t.key}
                    type="button"
                    class={[
                      'flowchat-turn-pill',
                      activeTurnIdx.value === t.msgIndex && 'active',
                      t.hasError && 'error',
                      t.streaming && 'streaming',
                    ]}
                    title={`T${t.turnNumber}: ${t.summary}`}
                    onClick={() => {
                      activeTurnIdx.value = t.msgIndex
                      props.onJumpTurn(t.msgIndex)
                    }}
                  >
                    <span class="flowchat-turn-pill-dot"></span>
                    <span class="flowchat-turn-pill-num">{t.turnNumber}</span>
                  </button>
                ))}
              </div>
              <button
                type="button"
                class="flowchat-rail-edge-btn"
                title="跳到底部"
                onClick={() => props.onScrollEdge('bottom')}
              >
                ↓
              </button>
            </aside>
          )}
        </div>
      )
    }
  },
})
