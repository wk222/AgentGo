import { defineComponent, ref, computed, nextTick, watch, onMounted, onBeforeUnmount } from 'vue'
import type { Message } from '../../stores/useChat'
import { useChatContext } from './chatContext'
import { sessionTitle, sessionIdOf } from '../../stores/useChat'
import { wailsCall } from '../../shared/wails'
import { locateDroppedFile, dropNotice, nativeFileDropEnabled, isImagePath, watchNativeFileDrops, mentionTag } from './droppedFiles'
import PromptSuggestions from './PromptSuggestions'
import FlowChatTranscript from './FlowChatTranscript'
import { harnessList, getHarness, harnessOf, type HarnessId } from '../../shared/harness'
import { notify, confirmDialog } from '../../shared/ui/notify'
import { objectReferencesPrompt } from '../../stores/workbenchContext'
import { forkBlocker, forkLineage, lineageChip, lineageText } from '../../stores/forkModel'
import { useLayout } from '../../stores/useLayout'
import './fork.css'
import {
  attachedChatFiles,
  attachedWorkbenchObjects, clearWorkbenchObjects,
  attachFileToChat,
  removeAttachedChatFile,
  clearAttachedChatFiles,
  stageState,
} from '../../stores/agentBridge'

interface PlanTaskItem {
  id: string
  subject: string
  status: 'pending' | 'in_progress' | 'completed'
}

function extractSessionPlanTasks(messages: Message[]): PlanTaskItem[] {
  const map = new Map<string, PlanTaskItem>()
  let autoId = 0
  for (const m of messages) {
    if (m.type !== 'tool_call' || !m.toolName || !m.arguments) continue
    const name = m.toolName.trim()
    try {
      const args = JSON.parse(m.arguments)
      if (name === 'TaskCreate') {
        autoId++
        let id = String(args.id || args.taskId || autoId)
        if (m.content) {
          const match = String(m.content).match(/(?:id|task)[#:\s"]*([a-zA-Z0-9_-]+)/i)
          if (match?.[1]) id = match[1]
        }
        const subject = String(args.subject || args.title || args.description || `步骤 ${autoId}`).trim()
        map.set(id, { id, subject, status: 'pending' })
      } else if (name === 'TaskUpdate') {
        const id = String(args.taskId || args.id || '')
        const status = String(args.status || '')
        if (status === 'deleted') {
          map.delete(id)
        } else {
          const prev = map.get(id) || { id: id || String(++autoId), subject: String(args.subject || `步骤 ${id}`), status: 'pending' as const }
          if (args.subject) prev.subject = String(args.subject)
          if (status === 'in_progress' || status === 'completed' || status === 'pending') prev.status = status
          map.set(prev.id, prev)
        }
      } else if ((name === 'write_todos' || name === 'todo_write') && Array.isArray(args.todos)) {
        map.clear()
        args.todos.forEach((t: any, idx: number) => {
          const id = String(t.id || idx + 1)
          const st = t.status === 'completed' || t.status === 'in_progress' ? t.status : 'pending'
          map.set(id, { id, subject: String(t.content || t.subject || t.title || `步骤 ${idx + 1}`), status: st })
        })
      }
    } catch {}
  }
  return Array.from(map.values())
}

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
  { cmd: '/review', label: '审查工作区改动', desc: '审查尚未提交的改动：只报本次引入的真问题，按 P0–P3 分级（命令后可写审查重点）', action: 'insert', insertText: '/review ' },
  { cmd: '/undo', label: '撤销智能体的文件修改', desc: '把本会话里智能体改过的文件恢复到修改前（默认只撤销最近一次；/undo all 撤销整个会话）。你之后自己改过的文件不会被覆盖', action: 'insert', insertText: '/undo ' },
  { cmd: '/compact', label: '整理会话上下文', desc: '提炼当前会话关键决策与状态，释放冗余上下文', action: 'insert', insertText: '请总结当前会话的核心进展、已确认结论与下一步待办事项。' },
  { cmd: '/new', label: '新建对话 (Ctrl+L)', desc: '立即开启一个干净的新会话', action: 'exec' },
  { cmd: '/fork', label: '分叉当前对话', desc: '把整条对话复制成一条新对话继续聊，原对话保持不动', action: 'exec' },
  { cmd: '/clear', label: '清空当前会话', desc: '清空当前会话中的全部历史消息', action: 'exec' },
]

const WELCOME_CARDS = [
  {
    title: '了解一个项目',
    cmd: '@Codebase',
    desc: '扫描当前仓库核心模块职责、关键调用链与改进切入点',
    prompt: '@Codebase 请梳理当前项目的核心模块架构、关键数据流与可优化点。',
  },
  {
    title: '规划一次改进',
    cmd: '/plan',
    desc: '先对齐目标、文件边界与验证步骤，再安全落地代码修改',
    prompt: '[Plan Mode] 我想优化当前项目的核心模块，请先列出分步计划与涉及文件。',
  },
  {
    title: '审查代码变更',
    cmd: '/review',
    desc: '读取未提交的改动，只报本次引入的真问题，按 P0–P3 分级',
    prompt: '/review',
  },
  {
    title: '检查项目状态',
    cmd: '/goal',
    desc: '设定明确验收准则，由 Agent 自主调用工具与测试直至通过',
    prompt: '[Goal Contract] 请检查当前工作区的编译与测试状态，若有问题请定位并修复。',
  },
]

export default defineComponent({
  name: 'ChatPanel',
  setup() {
    const ctx = useChatContext()
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

    watch(() => [ctx.messages.length, ctx.chatPaneKey], () => { void scrollToBottom() })

    // '' when the current profile x canvas is not a preset: then no card is highlighted.
    const activeHarness = computed(() => harnessOf(ctx.modeProfile, ctx.modeCanvas)?.id ?? '')

    const pendingInteractMsg = computed(() => {
      for (let i = ctx.messages.length - 1; i >= 0; i--) {
        const m = ctx.messages[i]
        if ((m.type === 'approval' || m.type === 'question') && !m.resolved) {
          return { msg: m, index: i }
        }
      }
      return null
    })

    const layout = useLayout()
    const taskChecklistOpen = ref(false)
    const planTasks = computed(() => extractSessionPlanTasks(ctx.messages))
    const isDragOverChat = ref(false)
    let dragDepth = 0

    const isExternalFileDrag = (e: DragEvent): boolean => {
      const types = e.dataTransfer?.types
      if (!types) return false
      const arr = Array.from(types)
      return arr.includes('Files') || arr.includes('application/agentgo-workspace-file')
    }

    const resetDragOverlay = () => {
      dragDepth = 0
      isDragOverChat.value = false
    }

    const handleChatDragEnter = (e: DragEvent) => {
      if (!isExternalFileDrag(e)) return
      e.preventDefault()
      dragDepth++
      isDragOverChat.value = true
    }

    const handleChatDragOver = (e: DragEvent) => {
      if (!isExternalFileDrag(e)) return
      e.preventDefault()
      if (e.dataTransfer) {
        e.dataTransfer.dropEffect = 'copy'
      }
      isDragOverChat.value = true
    }

    const handleChatDragLeave = (e: DragEvent) => {
      if (!isDragOverChat.value) return
      e.preventDefault()
      dragDepth = Math.max(0, dragDepth - 1)
      if (dragDepth === 0) {
        isDragOverChat.value = false
      }
    }

    const attachDroppedFile = async (file: File & { path?: string }) => {
      // A miss searches the usual folders for a few seconds: say so instead of staying silent.
      const slow = setTimeout(() => notify.info(`正在查找「${file.name}」在电脑上的位置…`), 800)
      try {
        const located = await locateDroppedFile(file)
        clearTimeout(slow)
        attachFileToChat(located.path, false)
        const n = dropNotice(file.name, located)
        notify[n.level](n.text)
      } catch (err: any) {
        clearTimeout(slow)
        notify.error(err?.message || `无法添加「${file.name}」`)
      }
    }

    const handleChatDrop = (e: DragEvent) => {
      if (!isExternalFileDrag(e) && !isDragOverChat.value) return
      e.preventDefault()
      resetDragOverlay()

      const dt = e.dataTransfer
      if (!dt) return
      // In the desktop window the Wails runtime listens further up and turns a file
      // drop into real paths (see watchNativeFileDrops): it has to see this event.
      const native = nativeFileDropEnabled() && Array.from(dt.types || []).includes('Files')
      if (!native) e.stopPropagation()

      // 1. From IDE FileTree custom MIME
      const customRaw = dt.getData('application/agentgo-workspace-file')
      if (customRaw) {
        try {
          const parsed = JSON.parse(customRaw)
          if (parsed?.path) {
            attachFileToChat(String(parsed.path), Boolean(parsed.is_dir))
            nextTick(() => textareaEl.value?.focus())
            return
          }
        } catch {}
      }

      // 2. From OS File Explorer (dropped FileList)
      if (dt.files && dt.files.length > 0) {
        // dt.items is only readable during the event, so note which entries are folders now.
        const isFolder = Array.from(dt.items || []).map(it => it.kind === 'file' && Boolean(it.webkitGetAsEntry?.()?.isDirectory))
        for (let i = 0; i < dt.files.length; i++) {
          const file = dt.files[i] as File & { path?: string }
          const isImage = Boolean(file.type && file.type.startsWith('image/'))
          // Everything but images arrives with its real path from the desktop host.
          if (native && !isImage) continue
          if (isFolder[i]) {
            notify.info(`「${file.name}」是文件夹，浏览器拿不到它的位置：请切换工作区到该文件夹，或在输入框直接写它的完整路径`)
            continue
          }
          if (isImage) {
            const reader = new FileReader()
            reader.onload = async (ev) => {
              const dataUrl = ev.target?.result as string
              if (!dataUrl) return
              const commaIdx = dataUrl.indexOf(',')
              if (commaIdx === -1) return
              try {
                const res = await wailsCall('UploadAttachment', dataUrl.substring(commaIdx + 1), file.type)
                if (res) pastedImages.value.push(res)
              } catch {}
            }
            reader.readAsDataURL(file)
          } else {
            void attachDroppedFile(file)
          }
        }
        nextTick(() => textareaEl.value?.focus())
        return
      }

      // 3. Plain text path or @mention
      const plain = (dt.getData('text/plain') || '').trim()
      if (plain) {
        if (plain.startsWith('@') || /^[a-zA-Z0-9_./\\:-]+$/.test(plain)) {
          attachFileToChat(plain, false)
          nextTick(() => textareaEl.value?.focus())
        } else {
          inputText.value = inputText.value ? `${inputText.value} ${plain}` : plain
        }
      }
    }

    const selectHarnessMode = (id: HarnessId) => {
      const h = getHarness(id)
      ctx.changeMode(h.profile, h.canvas)
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
        ctx.newChat()
        return
      }
      if (opt.cmd === '/clear') {
        inputText.value = ''
        ctx.clearChat()
        return
      }
      if (opt.cmd === '/fork') {
        inputText.value = ''
        doFork()
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

    watch(() => ctx.sessionId, () => { clearWorkbenchObjects(); clearAttachedChatFiles() })
    const handleSend = async () => {
      const rawText = inputText.value.trim()
      if (!rawText && pastedImages.value.length === 0 && attachedChatFiles.value.length === 0 && attachedWorkbenchObjects.value.length === 0) return
      if (rawText === '/new' || rawText === '/clear') {
        inputText.value = ''
        clearAttachedChatFiles()
        clearWorkbenchObjects()
        if (textareaEl.value) textareaEl.value.style.height = 'auto'
        if (rawText === '/new') ctx.newChat(); else ctx.clearChat()
        return
      }
      if (rawText === '/fork') {
        inputText.value = ''
        if (textareaEl.value) textareaEl.value.style.height = 'auto'
        doFork()
        return
      }

      let finalPrompt = objectReferencesPrompt(attachedWorkbenchObjects.value) + rawText
      if (attachedChatFiles.value.length > 0) {
        const missingRefs = attachedChatFiles.value
          .map(f => mentionTag(f.path))
          .filter(tag => !finalPrompt.includes(tag))
        if (missingRefs.length > 0) {
          finalPrompt = `${missingRefs.join(' ')}\n${finalPrompt || '请结合上述文件进行分析。'}`
        }
        clearAttachedChatFiles()
        clearWorkbenchObjects()
      }

      clearWorkbenchObjects()
      if (ctx.sending) {
        inputText.value = ''
        if (textareaEl.value) textareaEl.value.style.height = 'auto'
        await ctx.steer(finalPrompt)
        return
      }
      inputText.value = ''
      if (textareaEl.value) textareaEl.value.style.height = 'auto'
      const imgs = [...pastedImages.value]
      pastedImages.value = []
      ctx.send(finalPrompt, imgs)
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
              notify.error('上传图片失败: ' + err.message)
            }
          }
          reader.readAsDataURL(file)
        } else if (item.kind === 'file') {
          // A file copied in the file manager and pasted here.
          const file = item.getAsFile()
          if (!file) continue
          e.preventDefault()
          void attachDroppedFile(file)
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
        const res = await wailsCall('SearchFiles', '', 50)
        if (Array.isArray(res) && res.length > 0) {
          workspaceFiles.value = res
        } else {
          const list = await wailsCall('ListWorkspace', '')
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
          insertText: `${mentionTag(f)} `,
        }))
      return [...builtin, ...files]
    })

    const selectMention = (opt: MentionOption) => {
      const start = mentionStartIndex.value
      const curPos = textareaEl.value?.selectionStart || (start + 1 + mentionFilter.value.length)
      const before = inputText.value.slice(0, start)
      const after = inputText.value.slice(curPos)
      if (opt.id.startsWith('file:')) {
        attachFileToChat(opt.desc, false)
        inputText.value = before + after
        showMentionMenu.value = false
        nextTick(() => textareaEl.value?.focus())
        return
      }
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

    const currentSession = () => ctx.sessions.find(s =>
      (s.id || s.session_id || '') === ctx.sessionId,
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
    const doNew = () => { historyOpen.value = false; ctx.newChat() }

    // 分叉：现在不行就说为什么（按钮仍可点，点了出提示，而不是毫无反应）；行就整条复制并切过去。
    const forkBlock = computed(() => forkBlocker({
      sessionId: ctx.sessionId,
      sending: ctx.sending,
      awaiting: ctx.awaitingInteract,
      forking: ctx.forking,
      hasUserMessage: ctx.messages.some(m => m.role === 'user'),
    }))
    const doFork = () => {
      historyOpen.value = false
      const why = forkBlock.value
      if (why) { notify.info(why); return }
      ctx.forkChat()
    }
    const lineage = computed(() => forkLineage(currentSession() as any))
    const doClear = async () => {
      historyOpen.value = false
      if (!ctx.sessionId) return
      const ok = await confirmDialog('该会话的全部消息将被删除，不能恢复。', { title: '清除当前会话', okText: '清除', danger: true })
      if (ok) ctx.clearChat()
    }
    const globalKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey)) return
      const k = e.key.toLowerCase()
      if (k === 'l' && !e.shiftKey) { e.preventDefault(); doNew() }
      else if (k === 'l' && e.shiftKey) { e.preventDefault(); doClear() }
    }
    let stopNativeDrops: (() => void) | null = null
    let disposed = false
    onMounted(() => {
      window.addEventListener('keydown', globalKey)
      window.addEventListener('dragend', resetDragOverlay)
      window.addEventListener('drop', resetDragOverlay)
      // Desktop window: files dropped on the chat arrive here with their real paths.
      void watchNativeFileDrops(items => {
        for (const it of items) {
          if (!it.is_dir && isImagePath(it.path)) continue // images go through the image attachment path
          attachFileToChat(it.path, Boolean(it.is_dir))
        }
        nextTick(() => textareaEl.value?.focus())
      }).then(off => { if (disposed) off(); else stopNativeDrops = off })
    })
    onBeforeUnmount(() => {
      disposed = true
      stopNativeDrops?.()
      window.removeEventListener('keydown', globalKey)
      window.removeEventListener('dragend', resetDragOverlay)
      window.removeEventListener('drop', resetDragOverlay)
    })

    return () => {
      const hasMessages = ctx.messages.some(m =>
        m.type === 'aui' || m.role === 'user' || (m.role === 'assistant' && (m.content || '').trim() && !String(m.content).startsWith('新会话已就绪')), 
      )
      const isWelcome = !hasMessages && !ctx.sessionLoading

      const sess = currentSession()
      const title = sess ? sessionTitle(sess) : (ctx.sessionId ? '对话' : '新对话')

      return (
        <div
          data-file-drop-target="chat"
          style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden', position: 'relative' }}
          onDragenter={handleChatDragEnter}
          onDragover={handleChatDragOver}
          onDragleave={handleChatDragLeave}
          onDrop={handleChatDrop}
        >
          {/* Cursor-style Drag & Drop Overlay */}
          {isDragOverChat.value && (
            <div class="chat-drop-overlay" style={{ pointerEvents: 'none' }}>
              <div class="chat-drop-card">
                <div class="chat-drop-icon">📎</div>
                <div class="chat-drop-title">松开鼠标，将文件或目录加入对话上下文</div>
                <div class="chat-drop-sub">支持从右侧 Explorer 文件树或系统资源管理器直接拖入 (@Context)</div>
              </div>
            </div>
          )}

          {/* Compact Desktop Top bar */}
          <div class="chat-topbar">
            <span class="chat-topbar-title">{title}</span>
            {lineage.value && (lineage.value.gone ? (
              <span class="chat-fork-chip gone" title={lineageText(lineage.value)}>
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>
                <span>{lineageChip(lineage.value)}</span>
              </span>
            ) : (
              <button
                type="button"
                class="chat-fork-chip"
                title={`${lineageText(lineage.value)}\n点击回到原对话`}
                onClick={() => ctx.selectSession(lineage.value!.parentId)}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>
                <span>{lineageChip(lineage.value)}</span>
              </button>
            ))}
            <div class="chat-topbar-badges">
              <button
                type="button"
                class="chat-harness-badge active"
                style={{ cursor: 'pointer' }}
                onClick={() => layout.openContext('xray')}
                title="点击打开 Cache-First 上下文、记忆注入与 Skills 透视抽屉 (X-Ray)"
              >
                上下文
              </button>
            </div>
            <button class="topbar-btn topbar-btn-accent" onClick={doNew} title="新会话 (Ctrl+L，或输入 /new)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
            </button>
            <button
              class={['topbar-btn', forkBlock.value && 'is-blocked']}
              aria-disabled={forkBlock.value ? 'true' : undefined}
              onClick={doFork}
              title={forkBlock.value ? `现在不能分叉：${forkBlock.value}` : '分叉：把整条对话复制成新对话继续聊，原对话不动（或输入 /fork）'}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>
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
                      {ctx.sessions.length === 0 && <div class="chat-history-empty">暂无历史会话</div>}
                      {ctx.sessions.map((s) => {
                        const sid = sessionIdOf(s)
                        const active = sid === ctx.sessionId
                        return (
                          <div key={sid} class={['chat-history-item', active && 'active']}
                            onClick={() => { historyOpen.value = false; if (!active) ctx.selectSession(sid) }}>
                            <div class="chat-history-main">
                              <div class="chat-history-title">{sessionTitle(s)}</div>
                              <div class="chat-history-meta">{fmtTime((s as any).updated_at)}{(s as any).message_count ? ' · ' + (s as any).message_count + ' 条' : ''}</div>
                            </div>
                            <button class="chat-history-del" title="删除"
                              onClick={(e: MouseEvent) => { e.stopPropagation(); ctx.deleteSession(sid) }}>
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
            <button class="topbar-btn" onClick={ctx.reload} title="重新加载会话">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 .49-3.15"/></svg>
            </button>
            <button
              class="topbar-btn"
              onClick={ctx.toggleContext}
              title={ctx.contextOpen ? '收起上下文抽屉' : '打开上下文抽屉'}
            >
              {ctx.contextOpen
                ? <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M15 3v18"/></svg>
                : <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/></svg>
              }
            </button>
          </div>

          {/* Pinned PlanTask Checklist (surfaces Eino plantask middleware steps in-place) */}
          {planTasks.value.length > 0 && (
            <div class="chat-task-checklist">
              <button
                type="button"
                class="chat-task-checklist-head"
                onClick={() => { taskChecklistOpen.value = !taskChecklistOpen.value }}
              >
                <span class="chat-task-checklist-title">
                  📋 任务拆解进度 ({planTasks.value.filter(t => t.status === 'completed').length}/{planTasks.value.length})
                </span>
                <span class="chat-task-checklist-sub">
                  {(planTasks.value.find(t => t.status === 'in_progress') || planTasks.value.find(t => t.status === 'pending'))?.subject || '全部步骤已完成'}
                </span>
                <span class="chat-task-checklist-toggle">{taskChecklistOpen.value ? '▴' : '▾'}</span>
              </button>
              {taskChecklistOpen.value && (
                <div class="chat-task-checklist-body">
                  {planTasks.value.map(t => (
                    <div key={t.id} class={['chat-task-checklist-item', t.status]}>
                      <span class="chat-task-checklist-icon">
                        {t.status === 'completed' ? '✓' : t.status === 'in_progress' ? '●' : '○'}
                      </span>
                      <span class="chat-task-checklist-text">{t.subject}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {/* Thread */}
          <div class="chat-thread" ref={threadEl} key={ctx.chatPaneKey}>
            {ctx.sessionLoading && (
              <div class="chat-loading">
                <div class="chat-loading-dots">
                  <span></span><span></span><span></span>
                </div>
                <span>加载会话…</span>
              </div>
            )}

            {!ctx.sessionLoading && isWelcome && (
              <div class="chat-empty chat-welcome">
                <div class="chat-welcome-header">
                  <span class="chat-welcome-badge">你的桌面工作伙伴</span>
                  <h3>今天想一起完成什么？</h3>
                  <p>从一个问题开始，也可以交给我一项工作。</p>
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

            {!ctx.sessionLoading && !isWelcome && (
              <FlowChatTranscript
                messages={ctx.messages}
                formInputs={ctx.formInputs}
                onApprove={ctx.approve}
                onReject={ctx.reject}
                onSubmitAUI={ctx.submitAUI}
                onSubmitAUIAction={ctx.submitAUIAction}
                onSubmitQuestion={ctx.submitQuestion}
                onFormInput={ctx.setFormInput}
                onJumpTurn={jumpToMessageIndex}
                onScrollEdge={scrollEdge}
              />
            )}
          </div>

          {/* Run status */}
          {(ctx.sending || ctx.awaitingInteract) && ctx.runStatusLine && (
            <div class="run-status" role="status" aria-live="polite">
              <span class="run-status-dot"></span>
              <span>{ctx.runStatusLine}</span>
            </div>
          )}

          {/* Composer */}
          <div class="composer" style={{ position: 'relative' }}>
            {/* Slash 命令弹层 */}
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
                    onMousedown={(e: MouseEvent) => {
                      e.preventDefault()
                      selectSlashCommand(item)
                    }}
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
                    onMousedown={(e: MouseEvent) => {
                      e.preventDefault()
                      selectMention(item)
                    }}
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
              disabled={ctx.sending}
              onSelect={(prompt) => {
                inputText.value = prompt
                nextTick(() => textareaEl.value?.focus())
              }}
            />

            {/* 审批吸顶条：待确认项固定在输入框上方 */}
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
                        onMousedown={(e: MouseEvent) => e.preventDefault()}
                        onClick={() => ctx.approve(pendingInteractMsg.value!.msg.approval_id || '')}
                      >
                        批准
                      </button>
                      <button
                        type="button"
                        class="msg-approve-btn no"
                        onMousedown={(e: MouseEvent) => e.preventDefault()}
                        onClick={() => ctx.reject(pendingInteractMsg.value!.msg.approval_id || '')}
                      >
                        拒绝
                      </button>
                      <button
                        type="button"
                        class="msg-approve-btn"
                        style={{ background: 'transparent', border: '1px solid var(--border)', color: 'var(--text-dim)' }}
                        onMousedown={(e: MouseEvent) => e.preventDefault()}
                        onClick={() => jumpToMessageIndex(pendingInteractMsg.value!.index)}
                        title="滚动到审批卡片以修改参数或填写附言"
                      >
                        改参/附言
                      </button>
                    </>
                  ) : (
                    <>
                      <button
                        type="button"
                        class="msg-approve-btn ok"
                        onMousedown={(e: MouseEvent) => e.preventDefault()}
                        onClick={() => jumpToMessageIndex(pendingInteractMsg.value!.index)}
                      >
                        查看选项
                      </button>
                      <button
                        type="button"
                        class="msg-approve-btn"
                        style={{ background: 'transparent', border: '1px solid var(--border)', color: 'var(--text-dim)' }}
                        onMousedown={(e: MouseEvent) => e.preventDefault()}
                        onClick={() => {
                          pendingInteractMsg.value!.msg.resolved = true
                        }}
                        title="忽略此问题并在下方直接输入"
                      >
                        忽略
                      </button>
                    </>
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

            {attachedWorkbenchObjects.value.length > 0 && <div class="composer-context-bar" aria-label="已引用对象">
              {attachedWorkbenchObjects.value.map((object,index)=><span class="composer-ctx-pill" key={object.kind+object.id} title={object.id}>
                <span>{object.kind==='code'?'代码':object.kind==='memory'?'记忆':object.kind==='workflow'?'工作流':'应用'} · {object.label}{object.nodeId?' · '+object.nodeId:''}{object.dirty?'（未保存）':''}</span>
                <button type="button" class="composer-ctx-remove" aria-label={'移除引用'+object.label} onClick={()=>{attachedWorkbenchObjects.value=attachedWorkbenchObjects.value.filter((_,i)=>i!==index)}}>×</button>
              </span>)}
            </div>}
            {/* Cursor-style Attached @File Context Pills Bar */}
            {(attachedChatFiles.value.length > 0 || stageState.activeFilePath) && (
              <div class="composer-context-bar">
                {attachedChatFiles.value.map(file => (
                  <span
                    key={file.path}
                    class="composer-ctx-pill"
                    title={`点击在代码编辑器中查看 ${file.path}`}
                    onClick={() => {
                      stageState.activeFilePath = file.path
                      layout.setView('code')
                    }}
                  >
                    <span class="composer-ctx-icon">{file.isDir ? '📁' : '📄'}</span>
                    <span class="composer-ctx-name">@{file.name}</span>
                    {file.path !== file.name && (
                      <span class="composer-ctx-path">{file.path}</span>
                    )}
                    <button
                      type="button"
                      class="composer-ctx-del"
                      title="移除此文件引用"
                      onClick={(e: MouseEvent) => {
                        e.stopPropagation()
                        removeAttachedChatFile(file.path)
                      }}
                    >
                      ×
                    </button>
                  </span>
                ))}

                {stageState.activeFilePath &&
                  !attachedChatFiles.value.some(f => f.path === stageState.activeFilePath.replace(/\\/g, '/')) && (
                    <button
                      type="button"
                      class="composer-ctx-add-active"
                      title={`将当前编辑器打开的文件 (${stageState.activeFilePath}) 加入对话上下文`}
                      onClick={() => attachFileToChat(stageState.activeFilePath, false)}
                    >
                      + 引用当前文件 @{stageState.activeFilePath.replace(/\\/g, '/').split('/').pop()}
                    </button>
                  )}
              </div>
            )}

            <div class="composer-box" style={ctx.sending ? { borderColor: 'var(--warning)' } : undefined}>
              <textarea
                ref={textareaEl}
                class="composer-input"
                placeholder={ctx.sending ? '补充要求，回车发送…' : '发送消息，或用 @ 添加文件…'}
                rows={1}
                value={inputText.value}
                onInput={handleInput}
                onKeydown={handleKeydown}
                onPaste={handlePaste}
              ></textarea>
              <div class="composer-actions">
                <button
                  class="composer-btn"
                  title="添加上下文 (@ 引用文件/代码库/Git，或直接从右侧文件树拖拽文件到此)"
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
                {ctx.sending ? (
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
                    <button class="composer-send composer-stop" onClick={ctx.stop} title="停止生成">
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><rect x="3" y="3" width="18" height="18" rx="2"/></svg>
                    </button>
                  </div>
                ) : (
                  <button
                    class="composer-send"
                    onClick={handleSend}
                    disabled={!inputText.value.trim() && attachedChatFiles.value.length === 0 && pastedImages.value.length === 0 && attachedWorkbenchObjects.value.length === 0}
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
                <select class="chat-mode-dropdown" aria-label="协作模式" value={activeHarness.value} disabled={ctx.modeSaving} onChange={(e: Event) => selectHarnessMode((e.target as HTMLSelectElement).value as HarnessId)}>
                  {harnessList().map(h => <option key={h.id} value={h.id}>{h.label}</option>)}
                </select>
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
