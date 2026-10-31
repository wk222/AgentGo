// 由 wails.ts 机械拆分而来（见 docs/plan.md F6）。

export const isBrowser = typeof window !== 'undefined'
export const previewHost = isBrowser && ['127.0.0.1', 'localhost', '::1'].includes(window.location.hostname)
