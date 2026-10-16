// 前端 IPC 门面：保持 `import { wailsCall } from './wails'` 的旧路径不变。
// 实现已拆到 ./ipc/（runtime=真 Wails 运行时，mock/=浏览器预览用的假后端，call=串行队列与超时）。
export { wailsEvents, waitForWailsEvents } from './ipc/runtime'
export { wailsCall, wailsCallRaw, wailsCallUrgent, wailsCallWithTimeout, resetIpcQueue } from './ipc/call'
export type { IpcContract, IpcMethod } from './ipc/contract'
