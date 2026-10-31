import { defineComponent, ref, computed } from 'vue'
import ArtifactCard from './ArtifactCard'
import { parseArtifact } from './artifactModel'
import {
  Wrench,
  ChevronDown,
  ChevronRight,
  CheckCircle2,
  AlertCircle,
  Loader2,
  Copy,
  Check,
  FileCode2,
  ExternalLink,
} from 'lucide-vue-next'
import { stageState } from '../../stores/agentBridge'
import { useLayout } from '../../stores/useLayout'

const FILE_EDIT_TOOL_RE = /(write_file|edit_file|replace_in_file|str_replace|apply_patch|create_file)/i

interface DiffPreviewData {
  filePath: string
  added: number
  removed: number
  lines: Array<{ type: 'add' | 'del' | 'meta'; text: string }>
  truncated: number
}

function extractFileEditDiff(toolName: string, rawArgs: string): DiffPreviewData | null {
  if (!rawArgs || !FILE_EDIT_TOOL_RE.test(toolName || '')) return null
  try {
    const p = JSON.parse(rawArgs)
    if (!p || typeof p !== 'object') return null
    const filePath = String(
      p.path || p.file_path || p.filepath || p.target_file || p.filename || '',
    ).trim()
    if (!filePath) return null

    const oldStr =
      typeof p.old_string === 'string'
        ? p.old_string
        : typeof p.old_text === 'string'
          ? p.old_text
          : typeof p.search === 'string'
            ? p.search
            : null
    const newStr =
      typeof p.new_string === 'string'
        ? p.new_string
        : typeof p.new_text === 'string'
          ? p.new_text
          : typeof p.replace === 'string'
            ? p.replace
            : null

    const allLines: Array<{ type: 'add' | 'del' | 'meta'; text: string }> = []
    let added = 0
    let removed = 0

    if (oldStr !== null || newStr !== null) {
      const oldLines = (oldStr || '').split(/\r?\n/)
      const newLines = (newStr || '').split(/\r?\n/)
      if (oldStr) {
        removed = oldLines.length
        for (const l of oldLines) allLines.push({ type: 'del', text: `- ${l}` })
      }
      if (newStr) {
        added = newLines.length
        for (const l of newLines) allLines.push({ type: 'add', text: `+ ${l}` })
      }
    } else if (typeof p.content === 'string' || typeof p.contents === 'string') {
      const body = String(p.content ?? p.contents ?? '')
      const bodyLines = body.split(/\r?\n/)
      added = bodyLines.length
      for (const l of bodyLines) allLines.push({ type: 'add', text: `+ ${l}` })
    } else if (typeof p.patch === 'string' || typeof p.diff === 'string') {
      const patchLines = String(p.patch ?? p.diff ?? '').split(/\r?\n/)
      for (const l of patchLines) {
        if (l.startsWith('+') && !l.startsWith('+++')) {
          added++
          allLines.push({ type: 'add', text: l })
        } else if (l.startsWith('-') && !l.startsWith('---')) {
          removed++
          allLines.push({ type: 'del', text: l })
        } else if (l.startsWith('@@')) {
          allLines.push({ type: 'meta', text: l })
        }
      }
    }

    const MAX_PREVIEW = 12
    return {
      filePath,
      added,
      removed,
      lines: allLines.slice(0, MAX_PREVIEW),
      truncated: Math.max(0, allLines.length - MAX_PREVIEW),
    }
  } catch {
    return null
  }
}

export default defineComponent({
  name: 'ToolCallBubble',
  props: {
    toolName: { type: String, default: 'tool' },
    arguments: { type: String, default: '' },
    content: { type: String, default: '' },
    status: { type: String, default: 'completed' },
    sessionId: { type: String, default: '' },
  },
  setup(props) {
    const isExpanded = ref(false)
    const isCopied = ref(false)
    const showInlineDiff = ref(true)
    const layout = useLayout()

    const diffPreview = computed(() => extractFileEditDiff(props.toolName, props.arguments))

    const openInRightCanvas = (e: MouseEvent, filePath: string) => {
      e.stopPropagation()
      stageState.activeFilePath = ''
      requestAnimationFrame(() => {
        stageState.activeFilePath = filePath
        layout.setView('code')
      })
    }

    const copyOutput = async () => {
      if (!props.content) return
      try {
        await navigator.clipboard.writeText(props.content)
        isCopied.value = true
        setTimeout(() => {
          isCopied.value = false
        }, 1800)
      } catch {}
    }

    const formatArgs = (raw: string) => {
      if (!raw) return ''
      try {
        const parsed = JSON.parse(raw)
        return JSON.stringify(parsed, null, 2)
      } catch {
        return raw
      }
    }

    const summarizeInlineArg = (raw: string): string => {
      if (!raw) return ''
      try {
        const p = JSON.parse(raw)
        if (!p || typeof p !== 'object') return ''
        const candidate =
          p.path || p.file_path || p.filepath || p.command || p.cmd ||
          p.query || p.pattern || p.name || p.app_name || p.url || p.subject || ''
        const s = String(candidate || '').trim().replace(/\s+/g, ' ')
        return s.length > 56 ? `${s.slice(0, 56)}…` : s
      } catch {
        const s = raw.trim().replace(/\s+/g, ' ')
        return s.length > 48 ? `${s.slice(0, 48)}…` : s
      }
    }

    return () => {
      const isRunning = props.status === 'running' || props.status === 'in_progress'
      const isFailed = props.status === 'error' || props.status === 'failed'
      const isSuccess = !isRunning && !isFailed
      const argSummary = summarizeInlineArg(props.arguments)
      const diff = diffPreview.value
      const delivered = props.toolName === 'deliver_file' && isSuccess ? parseArtifact(props.content) : null

      return (
        <div class={['tool-call-card', isRunning && 'running', isFailed && 'failed', diff && 'has-file-diff']}>
          <div
            class="tool-call-header"
            role="button"
            tabindex={0}
            aria-expanded={isExpanded.value}
            onClick={() => (isExpanded.value = !isExpanded.value)}
            onKeydown={(e: KeyboardEvent) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                isExpanded.value = !isExpanded.value
              }
            }}
            title="点击展开或收起工具调用详情"
          >
            <div class="tool-call-main">
              <span class="tool-call-icon-box">
                {diff ? <FileCode2 size={12} /> : <Wrench size={12} />}
              </span>
              <span class="tool-call-name">{props.toolName}</span>
              {argSummary && (
                <code class="tool-call-arg-summary" title={argSummary}>{argSummary}</code>
              )}
              {diff && (diff.added > 0 || diff.removed > 0) && (
                <span class="tool-diff-stat-group" title={`新增 ${diff.added} 行 / 删除 ${diff.removed} 行`}>
                  {diff.added > 0 && <span class="tool-diff-stat add">+{diff.added}</span>}
                  {diff.removed > 0 && <span class="tool-diff-stat del">-{diff.removed}</span>}
                </span>
              )}
              <span
                class={[
                  'tool-status-pill',
                  isRunning ? 'running' : isFailed ? 'failed' : 'success',
                ]}
              >
                {isRunning && <Loader2 class="spin" size={11} />}
                {isSuccess && <CheckCircle2 size={11} />}
                {isFailed && <AlertCircle size={11} />}
                <span>{isRunning ? '运行中' : isFailed ? '失败' : '完成'}</span>
              </span>
            </div>

            <div class="tool-call-actions">
              {diff && (
                <button
                  type="button"
                  class="tool-open-file-btn"
                  title={`在右侧代码工作台打开 ${diff.filePath}`}
                  onClick={(e) => openInRightCanvas(e, diff.filePath)}
                >
                  <ExternalLink size={11} />
                  <span>打开</span>
                </button>
              )}
              {props.content && (
                <button
                  class="tool-copy-btn"
                  onClick={(e) => {
                    e.stopPropagation()
                    copyOutput()
                  }}
                  title="复制工具输出"
                >
                  {isCopied.value ? <Check size={12} /> : <Copy size={12} />}
                </button>
              )}
              <span class="tool-toggle-icon">
                {isExpanded.value ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              </span>
            </div>
          </div>

          {delivered && <ArtifactCard record={delivered} sessionId={props.sessionId} />}

          {diff && diff.lines.length > 0 && !isExpanded.value && (
            <div class="tool-inline-diff-box">
              <div class="tool-inline-diff-head" onClick={() => (showInlineDiff.value = !showInlineDiff.value)}>
                <span class="tool-inline-diff-path">{diff.filePath}</span>
                <span class="tool-inline-diff-toggle">{showInlineDiff.value ? '收起预览' : '展开变更预览'}</span>
              </div>
              {showInlineDiff.value && (
                <div class="tool-inline-diff-lines">
                  {diff.lines.map((line, idx) => (
                    <div key={idx} class={['tool-diff-line', `is-${line.type}`]}>
                      {line.text}
                    </div>
                  ))}
                  {diff.truncated > 0 && (
                    <div
                      class="tool-diff-more"
                      onClick={() => (isExpanded.value = true)}
                    >
                      … 还有 {diff.truncated} 行变更，点击展开完整参数与输出
                    </div>
                  )}
                </div>
              )}
            </div>
          )}

          {isExpanded.value && (
            <div class="tool-call-body">
              {props.arguments && (
                <div class="tool-call-section">
                  <div class="tool-section-label">输入参数</div>
                  <pre class="tool-code-block">
                    <code>{formatArgs(props.arguments)}</code>
                  </pre>
                </div>
              )}

              {props.content && (
                <div class="tool-call-section">
                  <div class="tool-section-label">执行结果</div>
                  <pre class="tool-code-block output-block">
                    <code>{formatArgs(props.content)}</code>
                  </pre>
                </div>
              )}
            </div>
          )}
        </div>
      )
    }
  },
})
