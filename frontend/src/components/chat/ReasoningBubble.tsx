import { defineComponent, ref, watch } from 'vue'
import { Brain, ChevronDown, ChevronRight, Loader2 } from 'lucide-vue-next'

export default defineComponent({
  name: 'ReasoningBubble',
  props: {
    thinking: { type: String, required: true },
    streaming: { type: Boolean, default: false },
  },
  setup(props) {
    // Keep completed reasoning compact; streaming reasoning still opens automatically.
    const isExpanded = ref(false)

    // Automatically expand while streaming thinking content
    watch(
      () => props.streaming,
      (streaming) => {
        if (streaming) isExpanded.value = true
      }
    )

    return () => {
      if (!props.thinking.trim()) return null

      return (
        <div class="reasoning-bubble">
          <div
            class="reasoning-bubble-header"
            onClick={() => (isExpanded.value = !isExpanded.value)}
            title="点击展开/折叠思考过程"
          >
            <div class="reasoning-bubble-title">
              <Brain class="reasoning-icon" size={14} />
              <span>深度思考 (Reasoning Process)</span>
              {props.streaming && (
                <span class="reasoning-live-badge">
                  <Loader2 class="reasoning-spin" size={12} />
                  <span>思考中…</span>
                </span>
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
