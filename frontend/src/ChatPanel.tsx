import { defineComponent, ref, computed, nextTick, watch, onMounted, onBeforeUnmount, PropType } from 'vue'
import type { Message, Session } from './composables/useChat'
import { sessionTitle, sessionIdOf } from './composables/useChat'
import { wailsCall } from './wails'
import PromptSuggestions from './components/chat/PromptSuggestions'
import FlowChatTranscript from './components/chat/FlowChatTranscript'
import { harnessList, getHarness, harnessOf, type HarnessId } from './harness'

interface SlashCommandOption {
  cmd: string
  label: string
  desc: string
  action: 'exec' | 'insert'
  insertText?: string
}

const SLASH_COMMANDS: SlashCommandOption[] = [
  { cmd: '/plan', label: '深度规划模式', desc: '切换至极致模式并要求先输出架构/修改计划再执行', action: 'insert', insertText: '[Plan Mode] 请先梳理架构边界、影响面与分步实施计划，确认后再修改代码：\n' },
  { cmd: '/goal', label: '目标契约驱动', desc: '按目标契约（Goal Contract）拆解任务并自主验证闭环', action: 'insert', insertText: '[Goal Contract] 目标与验收标准如下，请自主推进工具调用并验证结果：\n' },
  { cmd: '/review', label: '审查工作区改动', desc: '结合 @Git 检查当前未提交 Diff 的潜在缺陷与回归风险', action: 'insert', insertText: '@Git 请审查当前工作区的代码改动，指出潜在 Bug、边界遗漏与优化建议。' },
  { cmd: '/compact', label: '整理会话上下文', desc: '提炼当前会话关键决策与状态，释放冗余上下文', action: 'insert', insertText: '请总结当前会话的核心进展、已确认结论与下一步待办事项。' },
  { cmd: '/new', label: '新建对话 (Ctrl+L)', desc: '立即开启一个干净的新会话', action: 'exec' },
  { cmd: '/clear', label: '清空当前会话', desc: '清空当前会话中的全部历史消息', action: 'exec' },
]

const WELCOME_CARDS = [
  {
    title: '架构与上下文梳理',
    cmd: '@Codebase',
    desc: '扫描当前仓库核心模块职责、关键调用链与改进切入点',
    prompt: '@Codebase 请梳理当前项目的核心模块架构、关键数据流与可优化点。',
  },
  {
    title: '深度规划与重构方案',
    cmd: '/plan',
    desc: '先对齐目标、文件边界与验证步骤，再安全落地代码修改',
    prompt: '[Plan Mode] 我想优化当前项目的核心模块，请先列出分步计划与涉及文件。',
  },
  {
    title: 'Git 变更与风险审查',
    cmd: '/review',
    desc: '自动拉取工作区未提交 Diff，检查行为回归与边界漏洞',
    prompt: '@Git 请审查当前工作区的所有未提交改动，检查是否有潜在回归风险。',
  },
  {
    title: '目标契约闭环执行',
    cmd: '/goal',
    desc: '设定明确验收准则，由 Agent 自主调用工具与测试直至通过',
    prompt: '[Goal Contract] 请检查当前工作区的编译与测试状态，若有问题请定位并修复。',
  },
]

export default defineComponent({
  name: 'ChatPanel',
  props: {
    messages: { type: Array as PropType<Message[]>, required: true },
    sessionId: { type: String, default: '' },
    sessions: { type: Array as PropType<Session[]>, default: () => [] },
    sessionLoading: { type: Boolean, default: false },
    sending: { type: Boolean, default: false },
    awaitingInteract: { type: Boolean, default: false },
    runStatusLine: { type: String, default: '' },
    chatPaneKey: { type: Number, default: 0 },
    workspacePanelOpen: { type: Boolean, default: true },
    formInputs: { type: Object as PropType<Record<string, Record<string, any>>>, default: () => ({}) },
    onSend: { type: Function as PropType<(text: string, images?: string[]) => void>, required: true },
    onStop: { type: Function as PropType<() => void>, required: true },
    onToggleWorkspace: { type: Function as PropType<() => void>, required: true },
    onReload: { type: Function as PropType<() => void>, required: true },
    onNewChat: { type: Function as PropType<() => void>, default: () => {} },
    onClearChat: { type: Function as PropType<() => void>, default: () => {} },
    onSelectSession: { type: Function as PropType<(id: string) => void>, default: () => {} },
    onDeleteSession: { type: Function as PropType<(id: string) => void>, default: () => {} },
    onApprove: { type: Function as PropType<(id: string) => void>, required: true },
    onReject: { type: Function as PropType<(id: string) => void>, required: true },
    onSubmitAUI: { type: Function as PropType<(msg: Message, val: string, idx: number) => void>, required: true },
    onSubmitAUIAction: { type: Function as PropType<(msg: Message, val: string, idx: number) => void>, required: true },
    onSubmitQuestion: { type: Function as PropType<(msg: Message, answer: string, idx: number) => void>, required: true },
    onFormInput: { type: Function as PropType<(key: string, field: string, val: any) => void>, required: true },
    modeProfile: { type: String, default: 'assistant' },
    modeCanvas: { type: String, default: 'balanced' },
    modeSaving: { type: Boolean, default: false },
    onModeChange: { type: Function as PropType<(profile: string, canvas: string) => void>, required: true },
  },
  setup(props) {
    const inputText = ref('')
    const threadEl = ref<HTMLElement>()
    const textareaEl = ref<HTMLTextAreaElement>()
    const pastedImages = ref<string[]>([])

    const scrollToBottom = async () => {
      await nextTick()
      if (threadEl.value) threadEl.value.scrollTop = threadEl.value.scrollHeight
    }

    const scrollEdge = (edge: 'top' | 'bottom') => {
      if (!threadEl.value) return
      threadEl.value.scrollTo({
        top: edge === 'top' ? 0 : threadEl.value.scrollHeight,
        behavior: 'smooth',
      })
    }

    const jumpToMessageIndex = (msgIndex: number) => {
      if (!threadEl.value) return
      const target = threadEl.value.querySelector(`[data-msg-idx="${msgIndex}"]`) as HTMLElement | null
      if (target) {
        target.scrollIntoView({ behavior: 'smooth', block: 'start' })
      }
    }

    watch(() => [props.messages.length, props.chatPaneKey], () => { void scrollToBottom() })

    // '' when the current profile x canvas is not a preset: then no card is highlighted.
    const activeHarness = computed(() => harnessOf(props.modeProfile, props.modeCanvas)?.id ?? '')

    const pendingInteractMsg = computed(() => {
      for (let i = props.messages.length - 1; i >= 0; i--) {
        const m = props.messages[i]
        if ((m.type === 'approval' || m.type === 'question') && !m.resolved) {
          return { msg: m, index: i }
        }
      }
      return null
    })

    const selectHarnessMode = (id: HarnessId) => {
      const h = getHarness(id)
      props.onModeChange(h.profile, h.canvas)
    }

    // Slash Command Menu State
    const showSlashMenu = ref(false)
    const slashFilter = ref('')
    const slashIndex = ref(0)

    const filteredSlashCommands = computed(() => {
      const q = slashFilter.value.toLowerCase()
      return SLASH_COMMANDS.filter(item =>
        item.cmd.toLowerCase().includes(q) ||
        item.label.toLowerCase().includes(q) ||
        item.desc.toLowerCase().includes(q),
      )
    })

    const selectSlashCommand = (opt: SlashCommandOption) => {
      showSlashMenu.value = false
      if (opt.cmd === '/new') {
        inputText.value = ''
        props.onNewChat()
        return
      }
      if (opt.cmd === '/clear') {
        inputText.value = ''
        props.onClearChat()
        return
      }
      if (opt.cmd === '/plan' || opt.cmd === '/goal') {
        selectHarnessMode('ultimate')
      }
      inputText.value = opt.insertText || ''
      nextTick(() => {
        if (textareaEl.value) {
          textareaEl.value.focus()
          const len = inputText.value.length
          textareaEl.value.setSelectionRange(len, len)
        }
      })
    }

    const handleSend = async () => {
      const text = inputText.value.trim()
      if (!text && pastedImages.value.length === 0) return
      if (text === '/new' || text === '/clear') {
        inputText.value = ''
        if (textareaEl.value) textareaEl.value.style.height = 'auto'
        if (text === '/new') props.onNewChat(); else props.onClearChat()
        return
      }
      if (props.sending) {
        inputText.value = ''
        try {
          const res = await wailsCall<{ success?: boolean; steered?: boolean }>('SteerSession', props.sessionId, text)
          if (res?.steered) {
            console.info('[ChatPanel] Mid-turn steer queued:', text)
          }
        } catch (e) {
          console.error('[ChatPanel] SteerSession failed:', e)
        }
        return
      }
      inputText.value = ''
      if (textareaEl.value) textareaEl.value.style.height = 'auto'
      const imgs = [...pastedImages.value]
      pastedImages.value = []
      props.onSend(text, imgs)
    }

    const handlePaste = async (e: ClipboardEvent) => {
      const items = e.clipboardData?.items
      if (!items) return
      for (const item of items) {
        if (item.type.indexOf('image') !== -1) {
          const file = item.getAsFile()
          if (!file) continue
          const reader = new FileReader()
          reader.onload = async (event) => {
            const dataUrl = event.target?.result as string
            if (!dataUrl) return
            const commaIdx = dataUrl.indexOf(',')
            if (commaIdx === -1) return
            const base64Data = dataUrl.substring(commaIdx + 1)
            const mimeType = file.type
            try {
              const res = await wailsCall('UploadAttachment', base64Data, mimeType)
              if (res) {
                pastedImages.value.push(res)
              }
            } catch (err: any) {
              console.error('Failed to upload attachment:', err)
              alert('上传图片失败: ' + err.message)
            }
          }
          reader.readAsDataURL(file)
        }
      }
    }

    interface MentionOption {
      id: string
      label: string
      desc: string
      icon: string
      insertText: string
    }

    const showMentionMenu = ref(false)
    const mentionFilter = ref('')
    const mentionIndex = ref(0)
    const mentionStartIndex = ref(-1)
    const workspaceFiles = ref<string[]>([])

    const BUILTIN_MENTIONS: MentionOption[] = [
      { id: 'codebase', label: '@Codebase', desc: '检索整个工作区项目代码与语义上下文', icon: '⌘', insertText: '@Codebase ' },
      { id: 'git', label: '@Git', desc: '引入当前分支改动、未提交 Diff 与状态', icon: '⎇', insertText: '@Git ' },
      { id: 'terminal', label: '@Terminal', desc: '引入当前终端最近会话与运行输出', icon: '>_', insertText: '@Terminal ' },
      { id: 'problems', label: '@Problems', desc: '引入当前工作区代码错误与诊断信息', icon: '!', insertText: '@Problems ' },
    ]

    const fetchWorkspaceFiles = async () => {
      if (workspaceFiles.value.length > 0) return
      try {
        const res = await wailsCall<any>('SearchFiles', '', 50)
        if (Array.isArray(res) && res.length > 0) {
          workspaceFiles.value = res
        } else {
          const list = await wailsCall<any[]>('ListWorkspace', '')
          if (Array.isArray(list)) {
            workspaceFiles.value = list.map(item => item.path || item.name).filter(Boolean)
          }
        }
      } catch (e) {
        console.debug('[ChatPanel] fetchWorkspaceFiles failed:', e)
      }
    }

    const filteredMentions = computed(() => {
      const q = mentionFilter.value.toLowerCase()
      const builtin = BUILTIN_MENTIONS.filter(m =>
        m.label.toLowerCase().includes(q) || m.desc.toLowerCase().includes(q),
      )
      const files = workspaceFiles.value
        .filter(f => f.toLowerCase().includes(q))
        .slice(0, 10)
        .map(f => ({
          id: `file:${f}`,
          label: `@${f.split(/[\/\\]/).pop()}`,
          desc: f,
          icon: '·',
          insertText: `@${f} `,
        }))
      return [...builtin, ...files]
    })

    const selectMention = (opt: MentionOption) => {
      const start = mentionStartIndex.value
      const curPos = textareaEl.value?.selectionStart || (start + 1 + mentionFilter.value.length)
      const before = inputText.value.slice(0, start)
      const after = inputText.value.slice(curPos)
      inputText.value = before + opt.insertText + after
      showMentionMenu.value = false
      nextTick(() => {
        if (textareaEl.value) {
          const newPos = (before + opt.insertText).length
          textareaEl.value.focus()
          textareaEl.value.setSelectionRange(newPos, newPos)
        }
      })
    }

    const handleInput = (e: Event) => {
      const target = e.target as HTMLTextAreaElement
      inputText.value = target.value
      autoResize(e)

      const trimmed = target.value.trimStart()
      if (trimmed.startsWith('/') && !/\s/.test(trimmed)) {
        slashFilter.value = trimmed
        showSlashMenu.value = true
        slashIndex.value = 0
        showMentionMenu.value = false
        return
      }
      showSlashMenu.value = false

      const pos = target.selectionStart || 0
      const beforeCursor = target.value.slice(0, pos)
      const lastAt = beforeCursor.lastIndexOf('@')

      if (lastAt !== -1 && (lastAt === 0 || /\s/.test(beforeCursor[lastAt - 1]))) {
        const query = beforeCursor.slice(lastAt + 1)
        if (!/\s/.test(query)) {
          mentionFilter.value = query
          mentionStartIndex.value = lastAt
          showMentionMenu.value = true
          mentionIndex.value = 0
          fetchWorkspaceFiles()
          return
        }
      }
      showMentionMenu.value = false
    }

    const handleKeydown = (e: KeyboardEvent) => {
      if (showSlashMenu.value && filteredSlashCommands.value.length > 0) {
        if (e.key === 'ArrowDown') {
          e.preventDefault()
          slashIndex.value = (slashIndex.value + 1) % filteredSlashCommands.value.length
          return
        }
        if (e.key === 'ArrowUp') {
          e.preventDefault()
          slashIndex.value = (slashIndex.value - 1 + filteredSlashCommands.value.length) % filteredSlashCommands.value.length
          return
        }
        if (e.key === 'Enter' || e.key === 'Tab') {
          e.preventDefault()
          selectSlashCommand(filteredSlashCommands.value[slashIndex.value])
          return
        }
        if (e.key === 'Escape') {
          e.preventDefault()
          showSlashMenu.value = false
          return
        }
      }

      if (showMentionMenu.value && filteredMentions.value.length > 0) {
        if (e.key === 'ArrowDown') {
          e.preventDefault()
          mentionIndex.value = (mentionIndex.value + 1) % filteredMentions.value.length
          return
        }
        if (e.key === 'ArrowUp') {
          e.preventDefault()
          mentionIndex.value = (mentionIndex.value - 1 + filteredMentions.value.length) % filteredMentions.value.length
          return
        }
        if (e.key === 'Enter' || e.key === 'Tab') {
          e.preventDefault()
          selectMention(filteredMentions.value[mentionIndex.value])
          return
        }
        if (e.key === 'Escape') {
          e.preventDefault()
          showMentionMenu.value = false
          return
        }
      }

      if (e.key === 'Enter' && !e.shiftKey && !e.metaKey) {
        e.preventDefault()
        handleSend()
      }
    }

    const autoResize = (e: Event) => {
      const el = e.target as HTMLTextAreaElement
      el.style.height = 'auto'
      el.style.height = Math.min(el.scrollHeight, 160) + 'px'
    }

    const currentSession = () => props.sessions.find(s =>
      (s.id || s.session_id || '') === props.sessionId,
    )

    const historyOpen = ref(false)
    const fmtTime = (v: any): string => {
      if (v === undefined || v === null || v === '') return ''
      let ms = typeof v === 'number' ? v : Number(v)
      if (!isFinite(ms) || isNaN(ms)) ms = Date.parse(String(v))
      if (!isFinite(ms) || isNaN(ms)) return ''
      if (ms < 1e12) ms *= 1000
      const diff = Date.now() - ms
      const m = Math.floor(diff / 60000)
      if (m < 1) return '刚刚'
      if (m < 60) return m + ' 分钟前'
      const h = Math.floor(m / 60)
      if (h < 24) return h + ' 小时前'
      const d = Math.floor(h / 24)
      if (d < 30) return d + ' 天前'
      return new Date(ms).toLocaleDateString()
    }
    const doNew = () => { historyOpen.value = false; props.onNewChat() }
    const doClear = () => {
      historyOpen.value = false
      if (!props.sessionId) return
      if (window.confirm('清除当前会话？该会话的全部消息将被删除。')) props.onClearChat()
    }
    const globalKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey)) return
      const k = e.key.toLowerCase()
      if (k === 'l' && !e.shiftKey) { e.preventDefault(); doNew() }
      else if (k === 'l' && e.shiftKey) { e.preventDefault(); doClear() }
    }
    onMounted(() => window.addEventListener('keydown', globalKey))
    onBeforeUnmount(() => window.removeEventListener('keydown', globalKey))

    return () => {
      const hasMessages = props.messages.some(m =>
        m.type === 'aui' || m.role === 'user' || (m.role === 'assistant' && (m.content || '').trim()),
      )
      const isWelcome = !hasMessages && !props.sessionLoading

      const sess = currentSession()
      const title = sess ? sessionTitle(sess) : (props.sessionId ? '对话' : '新对话')

      return (
        <div style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
          {/* Compact Desktop Top bar */}
          <div class="chat-topbar">
            <span class="chat-topbar-title">{title}</span>
            <div class="chat-topbar-badges">
              <span class="chat-harness-badge active" title="已启用 AgentGo 前缀缓存与上下文分层保护">
                Cache-First
              </span>
            </div>
            <button class="topbar-btn topbar-btn-accent" onClick={doNew} title="新会话 (Ctrl+L，或输入 /new)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
            </button>
            <div class="chat-history-wrap">
              <button class={['topbar-btn', historyOpen.value && 'active']} onClick={() => { historyOpen.value = !historyOpen.value }} title="历史会话">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><polyline points="12 7 12 12 15 14"/></svg>
              </button>
              {historyOpen.value && (
                <>
                  <div class="chat-history-backdrop" onClick={() => { historyOpen.value = false }}></div>
                  <div class="chat-history-pop">
                    <div class="chat-history-head">
                      <span>最近会话</span>
                      <button class="chat-history-new" onClick={doNew}>+ 新会话</button>
                    </div>
                    <div class="chat-history-list">
                      {props.sessions.length === 0 && <div class="chat-history-empty">暂无历史会话</div>}
                      {props.sessions.map((s) => {
                        const sid = sessionIdOf(s)
                        const active = sid === props.sessionId
                        return (
                          <div key={sid} class={['chat-history-item', active && 'active']}
                            onClick={() => { historyOpen.value = false; if (!active) props.onSelectSession(sid) }}>
                            <div class="chat-history-main">
                              <div class="chat-history-title">{sessionTitle(s)}</div>
                              <div class="chat-history-meta">{fmtTime((s as any).updated_at)}{(s as any).message_count ? ' · ' + (s as any).message_count + ' 条' : ''}</div>
                            </div>
                            <button class="chat-history-del" title="删除"
                              onClick={(e: MouseEvent) => { e.stopPropagation(); props.onDeleteSession(sid) }}>
                              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14H6L5 6"/><path d="M10 11v6M14 11v6"/></svg>
                            </button>
                          </div>
                        )
                      })}
                    </div>
                  </div>
                </>
              )}
            </div>
            <button class="topbar-btn" onClick={doClear} title="清除当前会话 (Ctrl+Shift+L，或输入 /clear)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M19 6l-1 14H6L5 6"/></svg>
            </button>
            <button class="topbar-btn" onClick={props.onReload} title="重新加载会话">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 .49-3.15"/></svg>
            </button>
            <button
              class="topbar-btn"
              onClick={props.onToggleWorkspace}
              title={props.workspacePanelOpen ? '收起上下文抽屉' : '打开上下文抽屉'}
            >
              {props.workspacePanelOpen
                ? <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M15 3v18"/></svg>
                : <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/></svg>
              }
            </button>
          </div>

          {/* Thread */}
          <div class="chat-thread" ref={threadEl} key={props.chatPaneKey}>
            {props.sessionLoading && (
              <div class="chat-loading">
                <div class="chat-loading-dots">
                  <span></span><span></span><span></span>
                </div>
                <span>加载会话…</span>
              </div>
            )}

            {!props.sessionLoading && isWelcome && (
              <div class="chat-empty chat-welcome">
                <div class="chat-welcome-header">
                  <span class="chat-welcome-badge">AgentGo Native Runtime · Eino ADK</span>
                  <h3>AgentGo 工程控制台</h3>
                  <p>支持 @ 上下文注入、/ 斜杠模式切换、运行中实时纠偏 (Steer) 与前缀缓存优化</p>
                </div>
                <div class="chat-welcome-grid">
                  {WELCOME_CARDS.map((card, i) => (
                    <button
                      key={i}
                      type="button"
                      class="chat-welcome-card"
                      onClick={() => {
                        if (card.cmd === '/plan' || card.cmd === '/goal') selectHarnessMode('ultimate')
                        inputText.value = card.prompt
                        nextTick(() => textareaEl.value?.focus())
                      }}
                    >
                      <div class="chat-welcome-card-top">
                        <span class="chat-welcome-card-title">{card.title}</span>
                        <span class="chat-welcome-card-cmd">{card.cmd}</span>
                      </div>
                      <div class="chat-welcome-card-desc">{card.desc}</div>
                    </button>
                  ))}
                </div>
              </div>
            )}

            {!props.sessionLoading && !isWelcome && (
              <FlowChatTranscript
                messages={props.messages}
                formInputs={props.formInputs}
                onApprove={props.onApprove}
                onReject={props.onReject}
                onSubmitAUI={props.onSubmitAUI}
                onSubmitAUIAction={props.onSubmitAUIAction}
                onSubmitQuestion={props.onSubmitQuestion}
                onFormInput={props.onFormInput}
                onJumpTurn={jumpToMessageIndex}
                onScrollEdge={scrollEdge}
              />
            )}
          </div>

          {/* Run status */}
          {(props.sending || props.awaitingInteract) && props.runStatusLine && (
            <div class="run-status" role="status" aria-live="polite">
              <span class="run-status-dot"></span>
              <span>{props.runStatusLine}</span>
            </div>
          )}

          {/* Composer */}
          <div class="composer" style={{ position: 'relative' }}>
            {/* Slash Command Popover (Reasonix style) */}
            {showSlashMenu.value && filteredSlashCommands.value.length > 0 && (
              <div class="chat-composer-popover">
                <div class="chat-composer-popover-head">
                  <span>斜杠指令 (Slash Commands)</span>
                  <span>↑↓ 选择 · Enter 确认</span>
                </div>
                {filteredSlashCommands.value.map((item, idx) => (
                  <div
                    key={item.cmd}
                    class={['chat-composer-popover-item', idx === slashIndex.value && 'active']}
                    onClick={() => selectSlashCommand(item)}
                    onMouseenter={() => { slashIndex.value = idx }}
                  >
                    <div style={{ display: 'flex', alignItems: 'center', gap: '8px', minWidth: 0 }}>
                      <span class="chat-composer-popover-cmd">{item.cmd}</span>
                      <span style={{ fontWeight: 500, fontSize: '12px' }}>{item.label}</span>
                    </div>
                    <span class="chat-composer-popover-desc">{item.desc}</span>
                  </div>
                ))}
              </div>
            )}

            {/* Theme-aware @ Mention Popover */}
            {showMentionMenu.value && filteredMentions.value.length > 0 && (
              <div class="chat-composer-popover">
                <div class="chat-composer-popover-head">
                  <span>添加上下文引用 (@Context)</span>
                  <span>↑↓ 选择 · Enter 确认</span>
                </div>
                {filteredMentions.value.map((item, idx) => (
                  <div
                    key={item.id}
                    class={['chat-composer-popover-item', idx === mentionIndex.value && 'active']}
                    onClick={() => selectMention(item)}
                    onMouseenter={() => { mentionIndex.value = idx }}
                  >
                    <div style={{ display: 'flex', alignItems: 'center', gap: '8px', minWidth: 0 }}>
                      <span class="chat-composer-popover-cmd">{item.icon} {item.label}</span>
                    </div>
                    <span class="chat-composer-popover-desc">{item.desc}</span>
                  </div>
                ))}
              </div>
            )}

            <PromptSuggestions
              disabled={props.sending || props.awaitingInteract}
              onSelect={(prompt) => {
                inputText.value = prompt
                nextTick(() => textareaEl.value?.focus())
              }}
            />

            {/* OpenBitFun-style ChatInputApprovalBand: sticky pending confirmation bar above Composer */}
            {pendingInteractMsg.value && (
              <div class="chat-approval-band">
                <div class="chat-approval-band-info">
                  <span class="chat-approval-band-badge">
                    {pendingInteractMsg.value.msg.type === 'approval' ? '待审批' : '待回答'}
                  </span>
                  <span class="chat-approval-band-text">
                    {pendingInteractMsg.value.msg.content || 'Agent 正在等待您的确认以继续执行'}
                  </span>
                </div>
                <div class="chat-approval-band-actions">
                  {pendingInteractMsg.value.msg.type === 'approval' ? (
                    <>
                      <button
                        type="button"
                        class="msg-approve-btn ok"
                        onClick={() => props.onApprove(pendingInteractMsg.value!.msg.approval_id || '')}
                      >
                        批准
                      </button>
                      <button
                        type="button"
                        class="msg-approve-btn no"
                        onClick={() => props.onReject(pendingInteractMsg.value!.msg.approval_id || '')}
                      >
                        拒绝
                      </button>
                    </>
                  ) : (
                    <button
                      type="button"
                      class="msg-approve-btn ok"
                      onClick={() => jumpToMessageIndex(pendingInteractMsg.value!.index)}
                    >
                      查看并回答
                    </button>
                  )}
                </div>
              </div>
            )}

            {pastedImages.value.length > 0 && (
              <div class="composer-previews" style={{ display: 'flex', gap: '8px', padding: '4px 2px 8px', flexWrap: 'wrap' }}>
                {pastedImages.value.map((imgUrl, idx) => (
                  <div key={imgUrl} style={{ position: 'relative', width: '56px', height: '56px', borderRadius: 'var(--radius-md)', overflow: 'hidden', border: '1px solid var(--border)' }}>
                    <img src={imgUrl} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                    <button
                      onClick={() => pastedImages.value.splice(idx, 1)}
                      style={{ position: 'absolute', top: '2px', right: '2px', width: '16px', height: '16px', borderRadius: '50%', background: 'rgba(0,0,0,0.6)', color: '#fff', border: 'none', fontSize: '10px', display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer', padding: 0 }}
                      title="删除"
                    >✕</button>
                  </div>
                ))}
              </div>
            )}

            <div class="composer-box">
              <textarea
                ref={textareaEl}
                class="composer-input"
                placeholder="输入指令或问题… (@ 引用文件/Git，/ 调出模式指令，Enter 发送)"
                rows={1}
                value={inputText.value}
                onInput={handleInput}
                onKeydown={handleKeydown}
                onPaste={handlePaste}
                disabled={props.awaitingInteract}
              ></textarea>
              <div class="composer-actions">
                <button
                  class="composer-btn"
                  title="添加上下文 (@ 引用文件/代码库/Git)"
                  onClick={() => {
                    if (!showMentionMenu.value) {
                      showSlashMenu.value = false
                      showMentionMenu.value = true
                      mentionFilter.value = ''
                      mentionStartIndex.value = inputText.value.length
                      inputText.value += '@'
                      nextTick(() => {
                        textareaEl.value?.focus()
                        fetchWorkspaceFiles()
                      })
                    } else {
                      showMentionMenu.value = false
                    }
                  }}
                >
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"/><path d="M16 8v5a3 3 0 0 0 6 0v-1a10 10 0 1 0-3.92 7.94"/></svg>
                </button>
                {props.sending ? (
                  <div style={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                    {inputText.value.trim() && (
                      <button
                        class="composer-send"
                        onClick={handleSend}
                        title="发送中途纠偏指引 (Enter)"
                        style={{ backgroundColor: 'var(--warning)', color: '#fff', width: 'auto', padding: '0 8px', borderRadius: '4px' }}
                      >
                        <span style={{ fontSize: '11px', fontWeight: 600 }}>纠偏</span>
                      </button>
                    )}
                    <button class="composer-send composer-stop" onClick={props.onStop} title="停止生成">
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><rect x="3" y="3" width="18" height="18" rx="2"/></svg>
                    </button>
                  </div>
                ) : (
                  <button
                    class="composer-send"
                    onClick={handleSend}
                    disabled={!inputText.value.trim() || props.awaitingInteract}
                    title="发送 (Enter)"
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg>
                  </button>
                )}
              </div>
            </div>

            {/* AgentGo Four Collaboration Modes + Slash Trigger */}
            <div class="chat-composer-toolbar">
              <div class="chat-composer-pills">
                <div class="chat-pill-group" title="AgentGo 协作模式（极简 / 标准 / 极致 / 创造）">
                  {harnessList().map(h => (
                    <button
                      key={h.id}
                      type="button"
                      disabled={props.modeSaving}
                      title={h.desc}
                      class={['chat-pill-btn', activeHarness.value === h.id && 'active']}
                      onClick={() => selectHarnessMode(h.id)}
                    >
                      {h.label}
                    </button>
                  ))}
                </div>
              </div>
              <button
                type="button"
                class="chat-slash-trigger"
                onClick={() => {
                  showMentionMenu.value = false
                  showSlashMenu.value = !showSlashMenu.value
                  slashFilter.value = ''
                  slashIndex.value = 0
                  if (showSlashMenu.value) nextTick(() => textareaEl.value?.focus())
                }}
                title="查看斜杠命令 (/plan, /goal, /review, /compact)"
              >
                / 指令
              </button>
            </div>
          </div>
        </div>
      )
    }
  },
})
