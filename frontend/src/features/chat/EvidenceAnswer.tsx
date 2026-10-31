import { computed, defineComponent, ref, watch, type PropType } from 'vue'
import DOMPurify from 'dompurify'
import type { Message } from '../../stores/useChat'
import { decorateEvidenceHTML, evidenceForMessage, evidenceLocation } from './evidenceModel'
import './evidence.css'

export default defineComponent({
  name: 'EvidenceAnswer',
  props: {
    messages: { type: Array as PropType<Message[]>, required: true },
    index: { type: Number, required: true },
    html: String,
    text: String,
    streaming: Boolean,
  },
  setup(props) {
    const selectedID = ref('')
    watch(() => props.messages, () => { selectedID.value = '' })
    const records = computed(() => evidenceForMessage(props.messages, props.index))
    const selected = computed(() => records.value.get(selectedID.value))
    const html = computed(() => {
      const base = props.html ?? (props.text || '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      return decorateEvidenceHTML(DOMPurify.sanitize(base, { USE_PROFILES: { html: true } }), records.value)
    })
    function openSource(event: MouseEvent) {
      const el = event.target instanceof Element ? event.target.closest('button[data-evidence-id]') : null
      const id = el?.getAttribute('data-evidence-id') || ''
      if (records.value.has(id)) selectedID.value = id
    }
    return () => (
      <div class="evidence-answer">
        <div class={props.streaming ? 'msg-streaming' : ''} innerHTML={html.value} onClick={openSource} />
        {selected.value && (
          <section class="evidence-viewer" aria-label="引用原文">
            <div class="evidence-viewer-heading"><strong>引用原文</strong><button type="button" aria-label="关闭引用原文" onClick={() => { selectedID.value = '' }}>×</button></div>
            <div class="evidence-source">{selected.value.source}</div>
            <div class="evidence-location">{evidenceLocation(selected.value)}</div>
            <div class="evidence-snapshot">读取时的内容快照 · 版本 {selected.value.revision.slice(0, 12)}</div>
            <pre class="evidence-excerpt">{selected.value.text}</pre>
          </section>
        )}
      </div>
    )
  },
})
