// 非阻塞提示与确认：替代原生 alert()/confirm()。
// 视图只调用 notify.* / confirmDialog()；渲染由 <UiHost/> 负责（挂在应用根）。
import { reactive } from 'vue'

export type ToastKind = 'info' | 'success' | 'warning' | 'error'

export interface Toast {
  id: number
  kind: ToastKind
  text: string
}

export interface ConfirmRequest {
  id: number
  message: string
  title: string
  okText: string
  cancelText: string
  danger: boolean
  resolve: (ok: boolean) => void
}

const TOAST_MS: Record<ToastKind, number> = { info: 3500, success: 3000, warning: 6000, error: 8000 }
const MAX_TOASTS = 4

export const uiState = reactive({
  toasts: [] as Toast[],
  confirms: [] as ConfirmRequest[],
})

let seq = 0

function push(kind: ToastKind, text: string) {
  const msg = String(text ?? '').trim()
  if (!msg) return
  // 同一条文案连续出现只保留一条，避免批量失败时刷屏。
  if (uiState.toasts.some(t => t.kind === kind && t.text === msg)) return
  const id = ++seq
  uiState.toasts.push({ id, kind, text: msg })
  if (uiState.toasts.length > MAX_TOASTS) uiState.toasts.splice(0, uiState.toasts.length - MAX_TOASTS)
  setTimeout(() => dismissToast(id), TOAST_MS[kind])
}

export function dismissToast(id: number) {
  const i = uiState.toasts.findIndex(t => t.id === id)
  if (i >= 0) uiState.toasts.splice(i, 1)
}

export const notify = {
  info: (text: string) => push('info', text),
  success: (text: string) => push('success', text),
  warning: (text: string) => push('warning', text),
  error: (text: string) => push('error', text),
}

export interface ConfirmOptions {
  title?: string
  okText?: string
  cancelText?: string
  /** 破坏性操作：确认按钮用危险色，且默认焦点落在"取消"上。 */
  danger?: boolean
}

export function confirmDialog(message: string, opts: ConfirmOptions = {}): Promise<boolean> {
  return new Promise<boolean>(resolve => {
    uiState.confirms.push({
      id: ++seq,
      message,
      title: opts.title ?? '请确认',
      okText: opts.okText ?? '确定',
      cancelText: opts.cancelText ?? '取消',
      danger: !!opts.danger,
      resolve,
    })
  })
}

export function settleConfirm(id: number, ok: boolean) {
  const i = uiState.confirms.findIndex(c => c.id === id)
  if (i < 0) return
  const [req] = uiState.confirms.splice(i, 1)
  req.resolve(ok)
}
