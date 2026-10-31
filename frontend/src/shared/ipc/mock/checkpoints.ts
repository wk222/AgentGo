// 浏览器预览里的「按轮回退」：快照的是预览工作区（mockFiles），回答的形状与 Go 端
// （internal/bridge/workspace_checkpoint.go）一致，回退按钮、确认框、撤销条因此不依赖桌面运行时就能开发与自测。
// 和后端一样只存在内存里：刷新页面即失效。
import type { RollbackChange, RollbackPreview, RollbackResult, TurnCheckpoint } from '../../../stores/turnCheckpointModel'
import { appendMockMessage } from './state'
import { bytesOf, mockFiles } from './workspace'

interface Snapshot {
  id: string
  sessionID: string
  kind: 'turn' | 'pre_rollback'
  seq: number
  prompt: string
  createdAt: number
  files: Record<string, string>
}

const NOT_FOUND = 'checkpoint not found: it may have been recycled or the app was restarted'
const snapshots: Snapshot[] = []
let counter = 0

const clip = (s: string) => Array.from(s.replace(/\s+/g, ' ').trim()).slice(0, 80).join('')
// 和后端一样带时间戳：刷新页面后计数器归零，只靠自增会和旧消息里记的编号撞车
const newID = () => `cp_${Date.now()}_${(++counter).toString(16)}`

function info(s: Snapshot): TurnCheckpoint {
  return { id: s.id, kind: s.kind, seq: s.seq, prompt: s.prompt, created_at: s.createdAt, files: Object.keys(s.files).length, skipped: 0, root: 'preview' }
}

/** 一轮开始前存一份预览工作区；返回回退点编号（写进 started 事件和用户消息）。 */
export function beginMockCheckpoint(sessionID: string, prompt: string): string {
  const seq = snapshots.filter(s => s.sessionID === sessionID && s.kind === 'turn').length + 1
  const snap: Snapshot = { id: newID(), sessionID, kind: 'turn', seq, prompt: clip(prompt), createdAt: Date.now(), files: { ...mockFiles } }
  snapshots.push(snap)
  return snap.id
}

/** 预览里让「这一轮改了文件」：提问里带「改文件」时模拟 Agent 改一个、建一个、删一个，好让回退有东西可回。 */
export function applyMockTurnEdits(prompt: string) {
  if (!prompt.includes('改文件')) return
  mockFiles['README.md'] = `${mockFiles['README.md'] || ''}\n\n> 预览：这一轮改过这个文件。`
  mockFiles['notes/preview-turn.md'] = '# 预览\n\n这个文件是模拟的这一轮新建的。'
  delete mockFiles['data/large-dataset.csv']
}

export function listMockCheckpoints(sessionID: string) {
  const checkpoints = snapshots.filter(s => s.sessionID === sessionID && s.kind === 'turn').map(info)
  return { success: true, enabled: true, checkpoints, preview: true }
}

function find(sessionID: string, id: string): Snapshot | undefined {
  return snapshots.find(s => s.id === id && s.sessionID === sessionID)
}

function diff(snap: Snapshot): RollbackChange[] {
  const out: RollbackChange[] = []
  for (const path of [...new Set([...Object.keys(snap.files), ...Object.keys(mockFiles)])].sort()) {
    const was = snap.files[path]
    const now = mockFiles[path]
    if (was === now) continue
    out.push({
      path,
      name: path.split('/').pop() || path,
      action: was === undefined ? 'delete' : now === undefined ? 'recreate' : 'restore',
      before_size: now === undefined ? 0 : bytesOf(now),
      after_size: was === undefined ? 0 : bytesOf(was),
      skip: false,
    })
  }
  return out
}

const countOf = (changes: RollbackChange[], action: string) => changes.filter(c => c.action === action).length

// 注意：别处的预览回答带 preview: true 当"来自预览运行时"的标记，但这一条的 preview 是真实回答里的
// 预览对象（RollbackPreview），不能再当标记用。
export function previewMockRollback(sessionID: string, id: string, busy: string) {
  const snap = find(sessionID, id)
  if (!snap) return { success: false, error: NOT_FOUND }
  const changes = diff(snap)
  const preview: RollbackPreview = {
    checkpoint: info(snap), changes,
    restore: countOf(changes, 'restore'), recreate: countOf(changes, 'recreate'), delete: countOf(changes, 'delete'),
    skipped: 0, unverified: 0,
  }
  return { success: true, preview, more: 0, busy }
}

/** 与 Go 端 rollbackNote 同一句话（模型下一轮会读到）。 */
function noteText(snap: Snapshot, r: RollbackResult): string {
  const head = snap.kind === 'pre_rollback'
    ? '用户撤销了上一次回退，工作区文件已恢复到那次回退之前的状态'
    : snap.prompt ? `用户把工作区文件回退到了「${snap.prompt}」这一轮开始之前的状态` : '用户把工作区文件回退到了之前某一轮开始时的状态'
  return `【系统记录】${head}（还原 ${r.restored} 个、重建 ${r.recreated} 个、删除 ${r.deleted} 个文件）。此后对话中提到的文件改动可能已经不存在，继续之前请先重新读取相关文件确认现状。`
}

export function rollbackMockTurn(sessionID: string, id: string, busy: string) {
  if (busy) return { success: false, busy: true, error: busy }
  const snap = find(sessionID, id)
  if (!snap) return { success: false, error: NOT_FOUND }
  const changes = diff(snap)
  // 回退前先存一份现状，撤销就是回到它
  const undo: Snapshot = { id: newID(), sessionID, kind: 'pre_rollback', seq: 0, prompt: '', createdAt: Date.now(), files: { ...mockFiles } }
  snapshots.push(undo)
  for (const c of changes) {
    if (c.action === 'delete') delete mockFiles[c.path]
    else mockFiles[c.path] = snap.files[c.path]
  }
  const result: RollbackResult = {
    restored: countOf(changes, 'restore'), recreated: countOf(changes, 'recreate'), deleted: countOf(changes, 'delete'),
    skipped: [], failed: [], unverified: 0, undo_id: undo.id,
  }
  if (changes.length) {
    appendMockMessage(sessionID, { role: 'assistant', type: 'text', content: noteText(snap, result), kind: 'workspace_rollback', rollback_target: id, undo_checkpoint: undo.id })
  }
  return { success: true, result, undo_checkpoint_id: undo.id }
}
