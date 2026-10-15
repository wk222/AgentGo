import { defineComponent, ref, computed, PropType } from 'vue'
import type { Message } from '../../composables/useChat'
import ReasoningBubble from './ReasoningBubble'
import ToolCallBubble from './ToolCallBubble'
import AUICard from './AUICard'

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
 * Borrowed from OpenBitFun activityClassification.ts:
 * Classify low-risk observation / exploration tool calls so consecutive read-only
 * probes can be folded into a single Explore Capsule, while edits/commands/errors stay prominent.
 */
const EXPLORE_TOOL_PATTERN = /^(read|list|search|grep|glob|get|find|inspect|stat|view|query|fetch|ls|cat|head|tail|git_status|git_diff|git_log|memory_search|skill)/i
const MUTATING_TOOL_PATTERN = /(write|edit|replace|delete|remove|create|exec|bash|shell|run|build|deploy|commit|push|patch|install)/i

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
    if (msg.role === 'user' && !msg.type) {
      currentTurn++
      const rawText = (msg.content || '').trim().replace(/\s+/g, ' ')
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
    onApprove: { type: Function as PropType<(id: string) => void>, required: true },
    onReject: { type: Function as PropType<(id: string) => void>, required: true },
    onSubmitAUI: { type: Function as PropType<(msg: Message, val: string, idx: number) => void>, required: true },
    onSubmitAUIAction: { type: Function as PropType<(msg: Message, val: string, idx: number) => void>, required: true },
    onSubmitQuestion: { type: Function as PropType<(msg: Message, answer: string, idx: number) => void>, required: true },
    onFormInput: { type: Function as PropType<(key: string, field: string, val: any) => void>, required: true },
    onJumpTurn: { type: Function as PropType<(msgIndex: number) => void>, required: true },
    onScrollEdge: { type: Function as PropType<(edge: 'top' | 'bottom') => void>, required: true },
  },
  setup(props) {
    const expandedExploreKeys = ref<Record<string, boolean>>({})
    const questionInput = ref<Record<string, string>>({})
    const questionChoices = ref<Record<string, string[]>>({})
    const activeTurnIdx = ref<number>(0)

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
                return (
                  <div key={seg.key} class="msg-row assistant" data-msg-idx={index}>
                    <div class={['msg-approval', msg.resolved && 'resolved']}>
                      <div class="msg-approval-label">{msg.resolved ? '已处理' : '需要批准'}</div>
                      <div class="msg-approval-prompt">{msg.content || '需要您的批准才能继续'}</div>
                      {msg.resolved && <div class="msg-approval-state">Agent 已收到您的选择并继续执行。</div>}
                      <div class="msg-approval-actions">
                        <button class="msg-approve-btn ok" disabled={msg.resolved} onClick={() => props.onApprove(msg.approval_id || '')}>批准</button>
                        <button class="msg-approve-btn no" disabled={msg.resolved} onClick={() => props.onReject(msg.approval_id || '')}>拒绝</button>
                      </div>
                    </div>
                  </div>
                )
              }

              if (msg.type === 'question') {
                const key = msg.interactId || String(index)
                const qData = msg.data || {}
                const choices: any[] = Array.isArray(qData.choices) ? qData.choices : []
                const multiple = !!qData.multiple
                const freeText = qData.free_text !== false && qData.freeText !== false
                const selected = questionChoices.value[key] || []
                return (
                  <div key={seg.key} class="msg-row assistant" data-msg-idx={index}>
                    <div class={['msg-approval', msg.resolved && 'resolved']} style={{ borderLeftColor: 'var(--info)' }}>
                      <div class="msg-approval-label" style={{ color: 'var(--info)' }}>需要输入</div>
                      <div class="msg-approval-prompt">{msg.content || '请输入您的回答：'}</div>
                      {choices.length > 0 && (
                        <div class="msg-choice-list">
                          {choices.map((c: any, i: number) => {
                            const val = String(c.id || c.value || c.label || c.title || i)
                            const label = c.label || c.title || c.name || val
                            return multiple ? (
                              <label key={val} class="msg-choice-check">
                                <input
                                  type="checkbox"
                                  checked={selected.includes(val)}
                                  disabled={msg.resolved}
                                  onChange={() => toggleQuestionChoice(key, val)}
                                />
                                <span>{label}</span>
                              </label>
                            ) : (
                              <button
                                key={val}
                                class="msg-choice-btn"
                                disabled={msg.resolved}
                                onClick={() => props.onSubmitQuestion(msg, val, index)}
                              >{label}</button>
                            )
                          })}
                        </div>
                      )}
                      <div style={{ display: 'flex', gap: '8px' }}>
                        {freeText && (
                          <input
                            style={{ flex: 1, padding: '6px 10px', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)', background: 'var(--input-bg)', color: 'var(--text)', fontSize: '13px' }}
                            placeholder="输入回答…"
                            disabled={msg.resolved}
                            value={questionInput.value[key] || ''}
                            onInput={(e: Event) => questionInput.value[key] = (e.target as HTMLInputElement).value}
                            onKeydown={(e: KeyboardEvent) => {
                              if (e.key === 'Enter') props.onSubmitQuestion(msg, questionInput.value[key] || '', index)
                            }}
                          />
                        )}
                        <button
                          class="msg-approve-btn ok"
                          disabled={msg.resolved}
                          onClick={() => {
                            const answer = multiple ? JSON.stringify(selected) : (questionInput.value[key] || selected[0] || '')
                            props.onSubmitQuestion(msg, answer, index)
                          }}
                        >{msg.resolved ? '已提交' : '发送'}</button>
                      </div>
                    </div>
                  </div>
                )
              }

              if (msg.type === 'tool_call') {
                return (
                  <div key={seg.key} class="msg-row assistant tool-call-row" data-msg-idx={index}>
                    <ToolCallBubble
                      toolName={msg.toolName || 'tool'}
                      arguments={msg.arguments}
                      content={msg.content}
                      status={msg.status}
                    />
                  </div>
                )
              }

              const { thinking, main } = extractThinking(msg.content || '')
              return (
                <div
                  key={seg.key}
                  class={['msg-row', msg.role]}
                  data-msg-idx={index}
                  data-turn-idx={isUserTurnStart ? seg.turnIndex : undefined}
                >
                  {msg.role === 'user' ? (
                    <div class="msg-bubble user-bubble">
                      {msg.content}
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
                          {msg.html && !thinking ? (
                            <div class={msg.streaming ? 'msg-streaming' : ''} innerHTML={msg.html}></div>
                          ) : (
                            <span class={msg.streaming ? 'msg-streaming' : ''}>{main}</span>
                          )}
                        </div>
                      )}
                    </div>
                  )}
                  {msg.role === 'user' && msg.content && (
                    <button class="msg-copy" onClick={() => navigator.clipboard?.writeText(msg.content || '').catch(() => {})}>复制</button>
                  )}
                </div>
              )
            })}
          </div>

          {/* OpenBitFun-inspired Turn Rail: right-hand quick navigation rail for multi-turn sessions */}
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
