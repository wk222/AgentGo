// 由 wails.ts 机械拆分而来（见 docs/plan.md F6）。
import { MAX_PREVIEW_BYTES, PREVIEW_WORKSPACE_ROOT_KEY, nowISO, readJSON } from './state'
import { wailsCall } from '../call'

export const mockFiles: Record<string, string> = {
  'README.md': `# AgentGo

AgentGo 是一个本地桌面智能体客户端，当前 UI 由 Wails v3 + Vue/Vite 驱动。

这份内容来自 localhost mock runtime，用于验证右栏 Workspace 预览、加入上下文、让 Agent 解释与 diff 操作。`,
  'frontend/src/wails.ts': `// Wails v3 runtime lives at /wails/runtime.js in desktop builds.
// Localhost preview falls back to a mock runtime so UI work stays fast.
export async function wailsCall(method: string, ...params: any[]) {
  // Real runtime first, mock runtime second.
}`,
  'frontend/src/RightPanel.tsx': `export default defineComponent({
  name: 'RightPanel',
  setup() {
    // Workspace becomes the Agent context entrance.
  },
})`,
  'docs/eino-capabilities.md': `# Eino capabilities

P1: Workflow tool/bash/database nodes should reuse the governed ToolsNode path where possible.

Next: reduce bespoke orchestration code by leaning on maintained Eino compose primitives.`,
  'internal/bridge/app_ipc.go': `func (s *AppService) ListWorkspace(relPath string) []WorkspaceEntry {
  // Desktop runtime returns real workspace files.
}

func (s *AppService) MemoryFeedback(id string, signal string) map[string]any {
  // Memory maintenance endpoint.
}`,
  'data/large-dataset.csv': 'preview,line\n'.repeat(64),
}



export function bytesOf(text: string) {
  return new Blob([text]).size
}

export function fileEntry(path: string, gitStatus = '') {
  const name = path.split('/').pop() || path
  return { name, path, is_dir: false, size: bytesOf(mockFiles[path] || ''), mod_time: nowISO(), git_status: gitStatus }
}

export const mockWorkspace: Record<string, any[]> = {
  '': [
    { name: 'frontend', path: 'frontend', is_dir: true, size: 0, mod_time: nowISO() },
    { name: 'internal', path: 'internal', is_dir: true, size: 0, mod_time: nowISO() },
    { name: 'docs', path: 'docs', is_dir: true, size: 0, mod_time: nowISO() },
    { ...fileEntry('README.md', 'M') },
    { name: 'data', path: 'data', is_dir: true, size: 0, mod_time: nowISO() },
  ],
  'frontend': [
    { name: 'src', path: 'frontend/src', is_dir: true, size: 0, mod_time: nowISO() },
  ],
  'frontend/src': [
    fileEntry('frontend/src/wails.ts'),
    fileEntry('frontend/src/RightPanel.tsx', 'M'),
  ],
  'internal': [
    { name: 'bridge', path: 'internal/bridge', is_dir: true, size: 0, mod_time: nowISO() },
  ],
  'internal/bridge': [
    fileEntry('internal/bridge/app_ipc.go', 'M'),
  ],
  'docs': [
    fileEntry('docs/eino-capabilities.md'),
  ],
  'data': [
    { name: 'large-dataset.csv', path: 'data/large-dataset.csv', is_dir: false, size: MAX_PREVIEW_BYTES + 4096, mod_time: nowISO() },
  ],
}

export function cleanRel(input: any) {
  const parts = String(input || '')
    .replace(/\\/g, '/')
    .split('/')
    .filter(p => p && p !== '.' && p !== '..')
  return parts.join('/')
}

export function mockWorkspaceRoot() {
  return readJSON(PREVIEW_WORKSPACE_ROOT_KEY, 'C:\\Users\\wgk\\Documents\\GitHub\\agentgo')
}

export function mockWorkspaceName(root: string) {
  const parts = String(root || '').replace(/\\/g, '/').split('/').filter(Boolean)
  return parts[parts.length - 1] || 'Workspace'
}

export function mockWorkspaceInfo(root = mockWorkspaceRoot()) {
  const warning = /(?:^|[\\/])(Downloads|Documents|Desktop)$/i.test(root)
    ? '工作区范围偏大，建议选择具体项目目录，避免 Agent 拉入无关文件。'
    : ''
  return {
    success: true,
    root,
    name: mockWorkspaceName(root),
    git_root: root,
    branch: 'main',
    dirty_count: 3,
    is_git: true,
    writable: true,
    warning,
    preview_limit_bytes: MAX_PREVIEW_BYTES,
    watch_mode: 'poll',
    preview: true,
  }
}
