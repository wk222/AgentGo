// 由 wails.ts 机械拆分而来（见 docs/plan.md F6）。
import { emitMock } from './events'
import { appendMockMessage } from './state'

export const previewFlowgram = {
  nodes: [
    { id: 'start', type: 'Start', meta: { position: { x: 80, y: 120 } }, data: {} },
    { id: 'tool1', type: 'Tool', meta: { position: { x: 320, y: 120 } }, data: { tool: 'list_workspace', input: '{{input}}' } },
    { id: 'end', type: 'End', meta: { position: { x: 580, y: 120 } }, data: {} },
  ],
  edges: [
    { sourceNodeID: 'start', targetNodeID: 'tool1' },
    { sourceNodeID: 'tool1', targetNodeID: 'end' },
  ],
}

export async function mockStream(sessionID: string, text: string) {
  const answer = text.includes('上下文') || text.includes('解释')
    ? '已收到上下文。我会先识别文件意图、关键结构和可能风险，再给出面向改造的建议。'
    : '这是 localhost preview runtime 的模拟回复。你可以继续测试模式选择、A2UI 卡片、右侧上下文入口和各类面板操作。'
  appendMockMessage(sessionID, { role: 'user', type: 'text', content: text })
  const parts = answer.match(/.{1,10}/g) || [answer]
  emitMock('agent:trace', { component: 'Runner', phase: 'start', name: 'preview' })
  parts.forEach((delta, i) => {
    setTimeout(() => emitMock('chat:chunk', { session_id: sessionID, delta }), 80 + i * 70)
  })
  setTimeout(() => {
    appendMockMessage(sessionID, { role: 'assistant', type: 'text', content: answer })
    if (/aui|a2ui|卡片|预览|模式|面板/i.test(text)) {
      emitMock('a2ui:render', {
        session_id: sessionID,
        component: 'table',
        title: 'A2UI 预览数据',
        data: {
          rows: [
            { area: 'Topbar', status: 'mode wired', risk: 'narrow width' },
            { area: 'Workspace', status: 'context actions', risk: 'large file guard' },
            { area: 'Memory', status: 'feedback/gc/distill', risk: 'LLM unavailable in preview' },
          ],
        },
      })
    }
    emitMock('chat:done', { session_id: sessionID })
  }, 120 + parts.length * 80)
  return { success: true, streaming: true, session_id: sessionID, preview: true }
}

let previewEventSeq = 0
export function nextPreviewSeq() { return ++previewEventSeq }
export const previewRuns = new Map<string, { runID: string; sid: string; timers: ReturnType<typeof setTimeout>[]; approvalID?: string }>()

export function mockEngine(sessionID: string, text: string, resumed = false) {
  const old = previewRuns.get(sessionID)
  const run = resumed && old ? old : { runID: `preview-run-${Date.now()}`, sid: sessionID, timers: [] }
  previewRuns.set(sessionID, run)
  const emit = (type: string, payload: any) => emitMock('engine:event', {
    run_id: run.runID, session_id: sessionID, engine: 'preview', seq: ++previewEventSeq, type, payload,
  })
  const later = (delay: number, fn: () => void) => run.timers.push(setTimeout(fn, delay))
  if (!resumed) appendMockMessage(sessionID, { role: 'user', type: 'text', content: text })
  later(20, () => emit('status', { state: resumed ? 'resumed' : 'started' }))
  later(100, () => emit('tool_call', { id: 'preview-inspect', name: 'preview_inspect', arguments: '{"fixture":true}' }))
  later(230, () => emit('tool_result', { id: 'preview-inspect', name: 'preview_inspect', output: '界面预览模拟结果，未执行真实工具。', is_error: false }))
  const pending = !resumed && text.includes('审批')
  const answer = pending ? '预览检查完成，下一步等待你的审批。' : resumed
    ? '审批续跑的模拟文本已逐步返回，界面恢复完成。' : '统一事件流已连接。这是浏览器预览模拟回复，尚未调用真实模型或修改文件。'
  const parts = answer.match(/.{1,7}/g) || [answer]
  parts.forEach((delta, i) => later(300 + i * 140, () => emit('token', { delta })))
  later(350 + parts.length * 140, () => {
    const messages: any[] = [{ role: 'assistant', type: 'text', content: answer }]
    if (pending) {
      run.approvalID = `${run.runID}-approval`
      messages.push({ role: 'assistant', type: 'approval', approval_id: run.approvalID,
        tool_name: 'preview_apply', arguments: '{"fixture":true}', content: '批准模拟续跑？', status: 'pending' })
      emit('approval_request', { approval_id: run.approvalID, tool_name: 'preview_apply', prompt: '批准模拟续跑？', arguments: '{"fixture":true}' })
    }
    messages.forEach(m => appendMockMessage(sessionID, m))
    emit('done', { messages, pending, error: '' })
    if (!pending) previewRuns.delete(sessionID)
  })
  return { success: true, run_id: run.runID, session_id: sessionID, preview: true }
}
