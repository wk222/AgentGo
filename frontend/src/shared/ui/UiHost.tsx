import { defineComponent, onMounted, onUnmounted, watch, nextTick, ref } from 'vue'
import { X } from 'lucide-vue-next'
import { uiState, dismissToast, settleConfirm } from './notify'

// 应用根挂一个：渲染 toast 队列与确认对话框。
export default defineComponent({
  name: 'UiHost',
  setup() {
    const cancelBtn = ref<HTMLButtonElement | null>(null)
    const okBtn = ref<HTMLButtonElement | null>(null)

    const onKey = (e: KeyboardEvent) => {
      const top = uiState.confirms[uiState.confirms.length - 1]
      if (!top) return
      if (e.key === 'Escape') { e.preventDefault(); settleConfirm(top.id, false) }
      if (e.key === 'Enter') { e.preventDefault(); settleConfirm(top.id, true) }
    }
    onMounted(() => window.addEventListener('keydown', onKey, true))
    onUnmounted(() => window.removeEventListener('keydown', onKey, true))

    // 弹出时把焦点放到安全的按钮上：危险操作默认"取消"。
    watch(() => uiState.confirms.length, async n => {
      if (!n) return
      await nextTick()
      const top = uiState.confirms[uiState.confirms.length - 1]
      ;(top?.danger ? cancelBtn.value : okBtn.value)?.focus()
    })

    return () => {
      const top = uiState.confirms[uiState.confirms.length - 1]
      return (
        <>
          <div class="ui-toasts" role="status" aria-live="polite">
            {uiState.toasts.map(t => (
              <div key={t.id} class={['ui-toast', `ui-toast--${t.kind}`]}>
                <span class="ui-toast-text">{t.text}</span>
                <button class="ui-toast-close" aria-label="关闭" onClick={() => dismissToast(t.id)}>
                  <X size={12} />
                </button>
              </div>
            ))}
          </div>
          {top && (
            <div class="ui-modal-backdrop" onClick={() => settleConfirm(top.id, false)}>
              <div class="ui-modal" role="alertdialog" aria-modal="true" aria-label={top.title} onClick={(e: MouseEvent) => e.stopPropagation()}>
                <div class="ui-modal-title">{top.title}</div>
                <div class="ui-modal-body">{top.message}</div>
                <div class="ui-modal-actions">
                  <button ref={cancelBtn} class="ui-btn" onClick={() => settleConfirm(top.id, false)}>{top.cancelText}</button>
                  <button ref={okBtn} class={['ui-btn', top.danger ? 'ui-btn--danger' : 'ui-btn--primary']} onClick={() => settleConfirm(top.id, true)}>{top.okText}</button>
                </div>
              </div>
            </div>
          )}
        </>
      )
    }
  },
})
