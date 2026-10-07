import { defineComponent, computed, type PropType } from 'vue'
import { marked } from 'marked'
import DOMPurify from 'dompurify'

export const MarkdownPreview = defineComponent({
  name: 'MarkdownPreview',
  props: {
    content: { type: String, default: '' },
    fileName: { type: String, default: 'Document.md' },
  },
  setup(props) {
    const renderedHtml = computed(() => {
      try {
        const raw = marked.parse(props.content || '', {
          gfm: true,
          breaks: true,
        }) as string
        return DOMPurify.sanitize(raw)
      } catch (e) {
        return `<p style="color: red;">Markdown 解析失败</p>`
      }
    })

    return {
      renderedHtml,
    }
  },
  render() {
    return (
      <div
        style={{
          width: '100%',
          height: '100%',
          overflowY: 'auto',
          backgroundColor: '#18181B',
          color: '#E4E4E7',
          padding: '24px 32px',
          boxSizing: 'border-box',
          fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif',
          lineHeight: '1.6',
          fontSize: '14px',
        }}
      >
        <div
          class="markdown-body"
          innerHTML={this.renderedHtml}
          style={{
            maxWidth: '860px',
            margin: '0 auto',
          }}
        />
      </div>
    )
  },
})
