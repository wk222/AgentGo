import { defineComponent, ref, watch } from 'vue'
import { Brain, ChevronDown, ChevronRight, Loader2 } from 'lucide-vue-next'

export default defineComponent({
  name: 'ReasoningBubble',
  props: {
    thinking: { type: String, required: true },
    streaming: { type: Boolean, default: false },
  },
  setup(props) {
    // 思考正在流出时自动展开，让人看到它在想什么；想完自动收起成一行，点标题可再展开。
    // 用户手动点过之后就不再自动改变。
    const isExpanded = ref(props.streaming)
    let touched = false

    watch(
      () => props.streaming,
      (streaming) => {
        if (!touched) isExpanded.value = streaming
      },
    )

    const toggle = () => {
      touched = true
      isExpanded.value = !isExpanded.value
    }

    return () => {
      if (!props.thinking.trim()) return null
      const size = props.thinking.trim().length

      return (
        <div class={['reasoning-bubble', props.streaming && 'streaming']}>
          <div
            class="reasoning-bubble-header"
            onClick={toggle}
            title="点击展开/折叠思考过程"
          >
            <div class="reasoning-bubble-title">
              <Brain class="reasoning-icon" size={14} />
              <span>{props.streaming ? '思考中' : '思考过程'}</span>
              {props.streaming ? (
                <span class="reasoning-live-badge">
                  <Loader2 class="reasoning-spin" size={12} />
                  <span>{size} 字</span>
                </span>
              ) : (
                <span class="reasoning-size">{size} 字</span>
              )}
            </div>
            <div class="reasoning-bubble-toggle">
              {isExpanded.value ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
            </div>
          </div>

          {isExpanded.value && (
            <div class="reasoning-bubble-content">
              <pre class="reasoning-pre">
                <code>{props.thinking}</code>
              </pre>
            </div>
          )}
        </div>
      )
    }
  },
})
