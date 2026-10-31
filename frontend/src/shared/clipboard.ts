/**
 * 把文字放进剪贴板，成功返回 true。
 * 浏览器版常用局域网里的 http 地址打开，那不算「安全上下文」，navigator.clipboard 根本不存在，
 * 所以先试新接口，不行再退回临时文本框 + execCommand('copy')。
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // 权限被拒或页面不在前台：走下面的老办法
  }
  const box = document.createElement('textarea')
  box.value = text
  box.setAttribute('readonly', '')
  box.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;pointer-events:none'
  const focused = document.activeElement as HTMLElement | null
  document.body.appendChild(box)
  try {
    box.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    box.remove()
    focused?.focus?.()
  }
}
