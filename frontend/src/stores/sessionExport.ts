// 把一条对话导出成 Markdown 文件：后端写进 attachments 目录并给出下载地址，这里保存结果供对话框显示。
// 不自动触发下载：桌面版的 WebView 对程序触发的下载不一定有提示，用户亲手点「下载」最可靠，
// 同时给出文件路径，点不动也能在文件管理器里找到。
import { reactive } from 'vue'
import { wailsCallWithTimeout } from '../shared/wails'
import type { SessionExport } from '../shared/ipc/types'

export const exportState = reactive<{ busy: boolean; result: SessionExport | null }>({ busy: false, result: null })

/** 导出成功后 exportState.result 有值；失败时抛出带原因的错误。 */
export async function exportSession(sessionId: string): Promise<void> {
  const id = String(sessionId || '').trim()
  if (!id || exportState.busy) return
  exportState.busy = true
  try {
    const res = await wailsCallWithTimeout('ExportSession', 30000, id)
    if (res?.success === false || !res?.url) throw new Error(String(res?.error || '导出失败'))
    exportState.result = {
      name: String(res.name || ''),
      filename: String(res.filename || res.name || 'conversation.md'),
      path: String(res.path || ''),
      size: Number(res.size || 0),
      url: String(res.url),
      messages: Number(res.messages || 0),
    }
  } finally {
    exportState.busy = false
  }
}

export function closeExport() {
  exportState.result = null
}
