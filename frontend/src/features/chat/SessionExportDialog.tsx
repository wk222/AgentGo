import { defineComponent, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { Check, Copy, Download } from 'lucide-vue-next'
import { closeExport, exportState } from '../../stores/sessionExport'
import { formatBytes } from '../settings/usageModel'
import { notify } from '../../shared/ui/notify'
import './session-export.css'

// 对话导出完成后弹出：下载按钮由用户亲手点（桌面版 WebView 对程序触发的下载没有可靠提示），
// 同时给出文件路径，点不动也能在文件管理器里找到。
export default defineComponent({
  name: 'SessionExportDialog',
  setup() {
    const downloadLink = ref<HTMLAnchorElement | null>(null)
    const copied = ref(false)

    const onKey = (e: KeyboardEvent) => {
      if (exportState.result && e.key === 'Escape') { e.preventDefault(); closeExport() }
    }
    onMounted(() => window.addEventListener('keydown', onKey, true))
    onUnmounted(() => window.removeEventListener('keydown', onKey, true))

    watch(() => exportState.result, async r => {
      copied.value = false
      if (!r) return
      await nextTick()
      downloadLink.value?.focus()
    })

    const copyPath = async () => {
      const r = exportState.result
      if (!r) return
      try {
        await navigator.clipboard.writeText(r.path)
        copied.value = true
      } catch {
        notify.warning('无法写入剪贴板，请手动选中路径复制。')
      }
    }

    return () => {
      const r = exportState.result
      if (!r) return null
      return <div class="ui-modal-backdrop" onClick={closeExport}>
        <div class="ui-modal export-dialog" role="dialog" aria-modal="true" aria-label="对话已导出" onClick={(e: MouseEvent) => e.stopPropagation()}>
          <div class="ui-modal-title export-dialog-title"><Check size={16} />对话已导出</div>
          <div class="ui-modal-body">
            <div class="export-dialog-file" title={r.filename}>{r.filename}</div>
            <div class="export-dialog-meta">{r.messages} 条消息 · {formatBytes(r.size)} · Markdown 文本，可用记事本、Word、WPS 打开</div>
            <div class="export-dialog-hint">点「下载」选择保存位置；也可以直接到这里取：</div>
            <code class="export-dialog-path" title={r.path}>{r.path}</code>
          </div>
          <div class="ui-modal-actions">
            <button class="ui-btn" onClick={copyPath}>{copied.value ? <Check size={13} /> : <Copy size={13} />}{copied.value ? '已复制路径' : '复制路径'}</button>
            <button class="ui-btn" onClick={closeExport}>关闭</button>
            <a ref={downloadLink} class="ui-btn ui-btn--primary" href={r.url} download={r.filename} onClick={() => setTimeout(closeExport, 400)}><Download size={13} />下载</a>
          </div>
        </div>
      </div>
    }
  },
})
