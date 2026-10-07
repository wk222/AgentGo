import { defineComponent, ref } from 'vue'
import { Wrench, ChevronDown, ChevronRight, CheckCircle2, AlertCircle, Loader2, Copy, Check } from 'lucide-vue-next'

export default defineComponent({
  name: 'ToolCallBubble',
  props: {
    toolName: { type: String, default: 'tool' },
    arguments: { type: String, default: '' },
    content: { type: String, default: '' },
    status: { type: String, default: 'completed' },
  },
  setup(props) {
    const isExpanded = ref(false)
    const isCopied = ref(false)

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

    return () => {
      const isRunning = props.status === 'running' || props.status === 'in_progress'
      const isFailed = props.status === 'error' || props.status === 'failed'
      const isSuccess = !isRunning && !isFailed

      return (
        <div class={['tool-call-card', isRunning && 'running', isFailed && 'failed']}>
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
            title="点击查看工具调用详情"
          >
            <div class="tool-call-main">
              <span class="tool-call-icon-box">
                <Wrench size={13} />
              </span>
              <span class="tool-call-name">{props.toolName}</span>
              <span
                class={[
                  'tool-status-pill',
                  isRunning ? 'running' : isFailed ? 'failed' : 'success',
                ]}
              >
                {isRunning && <Loader2 class="spin" size={11} />}
                {isSuccess && <CheckCircle2 size={11} />}
                {isFailed && <AlertCircle size={11} />}
                <span>{isRunning ? '调用中…' : isFailed ? '执行失败' : '执行完成'}</span>
              </span>
            </div>

            <div class="tool-call-actions">
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
                  <pre class="tool-code-block tool-output">
                    <code>{props.content}</code>
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
