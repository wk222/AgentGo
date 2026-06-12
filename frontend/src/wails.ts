// Wails v3 serves the native bridge as an ES module at /wails/runtime.js.
// Plain Vite localhost does not serve it, so this file also provides a small
// preview runtime for UI development.
const SVC = 'agentgo/internal/bridge.AppService'
const WAILS_RUNTIME_PATH = '/wails/runtime.js'
const importRuntime = new Function('path', 'return import(path)') as (path: string) => Promise<any>

type WailsRuntime = { Call: any; Events: any }
type EventHandler = (payload: any) => void
type MockSession = {
  id: string
  title: string
  updated_at: string
  message_count: number
  messages: any[]
}

const PREVIEW_SESSIONS_KEY = 'agentgo-preview-sessions-v2'
const PREVIEW_MODE_KEY = 'agentgo-preview-mode'
const PREVIEW_GLOBAL_MODE_KEY = 'agentgo-preview-global-mode'
const PREVIEW_LLM_KEY = 'agentgo-preview-llm'
const PREVIEW_WORKSPACE_ROOT_KEY = 'agentgo-preview-workspace-root'
const MAX_PREVIEW_BYTES = 512 * 1024

let _rt: WailsRuntime | null = null
let _rtPromise: Promise<WailsRuntime | null> | null = null
let previewLogged = false

const isBrowser = typeof window !== 'undefined'
const previewHost = isBrowser && ['127.0.0.1', 'localhost', '::1'].includes(window.location.hostname)

function logPreviewOnce() {
  if (previewLogged) return
  previewLogged = true
  console.info('[AgentGo] 使用 localhost mock runtime；桌面版会自动切回 Wails v3 runtime。')
}

function loadRuntime(maxMs = 2500): Promise<WailsRuntime | null> {
  if (_rt) return Promise.resolve(_rt)
  if (_rtPromise) return _rtPromise

  _rtPromise = (async () => {
    const effectiveMaxMs = previewHost ? Math.min(maxMs, 350) : maxMs
    const deadline = Date.now() + effectiveMaxMs
    while (Date.now() < deadline) {
      try {
        const mod = await importRuntime(WAILS_RUNTIME_PATH)
        if (mod?.Call?.ByName) {
          _rt = mod as WailsRuntime
          return _rt
        }
      } catch {
        // Browser preview: /wails/runtime.js is not served by Vite.
      }
      await new Promise(r => setTimeout(r, 120))
    }
    return null
  })()

  return _rtPromise
}

const mockHandlers = new Map<string, Set<EventHandler>>()

const mockEvents = {
  On(event: string, cb: EventHandler) {
    const set = mockHandlers.get(event) || new Set<EventHandler>()
    set.add(cb)
    mockHandlers.set(event, set)
    return () => set.delete(cb)
  },
}

function emitMock(event: string, payload: any = {}) {
  const set = mockHandlers.get(event)
  if (!set?.size) return
  for (const cb of Array.from(set)) {
    try { cb(payload) } catch (e) { console.error('[AgentGo] mock event handler:', e) }
  }
}

export function wailsEvents(): { On: (...a: any[]) => any } | null {
  if (_rt?.Events?.On) return _rt.Events
  if (previewHost) {
    logPreviewOnce()
    return mockEvents
  }
  return null
}

export async function waitForWailsEvents(maxMs = 3000) {
  const rt = await loadRuntime(maxMs)
  if (rt?.Events?.On) return rt.Events
  logPreviewOnce()
  return mockEvents
}

let ipcQueue: Promise<any> = Promise.resolve()

export function resetIpcQueue() {
  ipcQueue = Promise.resolve()
}

function nowISO() {
  return new Date().toISOString()
}

function clone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v))
}

function readJSON<T>(key: string, fallback: T): T {
  if (!isBrowser) return fallback
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return fallback
    return JSON.parse(raw)
  } catch {
    return fallback
  }
}

function writeJSON(key: string, value: any) {
  if (!isBrowser) return
  try { localStorage.setItem(key, JSON.stringify(value)) } catch {}
}

function seedSessions(): MockSession[] {
  const ts = nowISO()
  const messages = [
    {
      role: 'assistant',
      type: 'text',
      content: '这是 localhost 预览模式。没有 Wails 桌面容器时，也可以调试会话、模式选择、A2UI 卡片和右侧 Workspace 面板。',
    },
    {
      role: 'assistant',
      type: 'aui',
      component: 'metrics',
      content: 'AgentGo 预览指标',
      data: {
        metrics: [
          { label: '上下文文件', value: 3, change: 1 },
          { label: '可用 Skills', value: 5, change: 2 },
          { label: '内置应用', value: 4, change: 1 },
        ],
      },
    },
    {
      role: 'assistant',
      type: 'aui',
      component: 'list',
      content: '建议下一步',
      data: {
        items: [
          { title: '从右栏选择文件并加入上下文' },
          { title: '切换 Assistant / Admin 与 Focused / Deep 模式' },
          { title: '打开 Memory、Skills、Apps 面板执行维护动作' },
        ],
        actions: [{ label: '已了解', value: 'ok' }],
      },
      interact_id: 'preview-list-action',
    },
  ]
  return [{
    id: 'preview-session',
    title: 'localhost 预览会话',
    updated_at: ts,
    message_count: messages.length,
    messages,
  }]
}

function getMockSessions(): MockSession[] {
  const sessions = readJSON<MockSession[]>(PREVIEW_SESSIONS_KEY, [])
  if (Array.isArray(sessions) && sessions.length) {
    return sessions.map(s => ({
      ...s,
      messages: Array.isArray(s.messages) ? s.messages : [],
      message_count: Array.isArray(s.messages) ? s.messages.length : Number(s.message_count || 0),
    }))
  }
  const seeded = seedSessions()
  writeJSON(PREVIEW_SESSIONS_KEY, seeded)
  return seeded
}

function saveMockSessions(sessions: MockSession[]) {
  writeJSON(PREVIEW_SESSIONS_KEY, sessions.map(s => ({
    ...s,
    message_count: Array.isArray(s.messages) ? s.messages.length : Number(s.message_count || 0),
    updated_at: s.updated_at || nowISO(),
  })))
}

function publicSessions() {
  return getMockSessions().map(({ messages, ...s }) => ({
    ...s,
    message_count: messages.length,
  }))
}

function ensureSession(id?: string): MockSession {
  const sessions = getMockSessions()
  const sid = String(id || '').trim()
  let session = sessions.find(s => s.id === sid)
  if (!session) {
    session = {
      id: sid || `preview-${Date.now()}`,
      title: '新对话',
      updated_at: nowISO(),
      message_count: 0,
      messages: [],
    }
    sessions.unshift(session)
    saveMockSessions(sessions)
  }
  return session
}

function appendMockMessage(sessionID: string, msg: any) {
  const sessions = getMockSessions()
  let session = sessions.find(s => s.id === sessionID)
  if (!session) {
    session = ensureSession(sessionID)
    sessions.unshift(session)
  }
  session.messages.push(msg)
  session.message_count = session.messages.length
  session.updated_at = nowISO()
  saveMockSessions(sessions)
}

function modeKey(sessionID: string) {
  return `${PREVIEW_MODE_KEY}:${sessionID}`
}

function defaultMode() {
  return readJSON(PREVIEW_GLOBAL_MODE_KEY, { profile: 'assistant', canvas: 'balanced' })
}

function getSessionMode(sessionID: string) {
  return readJSON(modeKey(sessionID), defaultMode())
}

const mockFiles: Record<string, string> = {
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

function bytesOf(text: string) {
  return new Blob([text]).size
}

function fileEntry(path: string, gitStatus = '') {
  const name = path.split('/').pop() || path
  return { name, path, is_dir: false, size: bytesOf(mockFiles[path] || ''), mod_time: nowISO(), git_status: gitStatus }
}

const mockWorkspace: Record<string, any[]> = {
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

function cleanRel(input: any) {
  const parts = String(input || '')
    .replace(/\\/g, '/')
    .split('/')
    .filter(p => p && p !== '.' && p !== '..')
  return parts.join('/')
}

function mockWorkspaceRoot() {
  return readJSON(PREVIEW_WORKSPACE_ROOT_KEY, 'C:\\Users\\wgk\\Documents\\GitHub\\agentgo')
}

function mockWorkspaceName(root: string) {
  const parts = String(root || '').replace(/\\/g, '/').split('/').filter(Boolean)
  return parts[parts.length - 1] || 'Workspace'
}

function mockWorkspaceInfo(root = mockWorkspaceRoot()) {
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

function mockCapabilityGrants() {
  return [
    { id: 'tool:execute_bash:agent', kind: 'tool', name: 'execute_bash', scope: 'agent', status: 'published', risk_level: 'high', reusable: true, recommended: false, verify_result: 'pass', metadata: { risk: 'high' } },
    { id: 'tool:list_workspace_dir:agent', kind: 'tool', name: 'list_workspace_dir', scope: 'agent', status: 'published', risk_level: 'low', reusable: true, recommended: true, verify_result: 'pass', metadata: { workspace: 'locked' } },
    { id: 'workflow:wf_preview_happy_path:global', kind: 'workflow', name: 'wf_preview_happy_path', scope: 'global', status: 'published', risk_level: 'medium', reusable: true, recommended: true, verify_result: 'skip', metadata: { source: 'preview' } },
    { id: 'app:dataset-profiler:inner', kind: 'app', name: 'dataset-profiler', scope: 'inner', status: 'published', risk_level: 'low', reusable: true, recommended: true, verify_result: 'pass', metadata: { kind: 'ui' } },
    { id: 'skill:eino_architecture:project', kind: 'skill', name: 'eino_architecture', scope: 'project', status: 'verified', risk_level: 'low', reusable: true, recommended: true, verify_result: 'pass', metadata: { path: 'skills/eino_architecture.md' } },
    { id: 'channel:preview:browser', kind: 'channel', name: 'preview', scope: 'browser', status: 'published', risk_level: 'low', reusable: false, recommended: false, verify_result: 'pass', metadata: { state: 'active' } },
  ]
}

const mockMemories = [
  { id: 'mem-ui-context', content: '用户希望右侧 Workspace 成为 Agent 上下文入口。', scope: 'agent', modality: 'fact', status: 'active', importance: 1.2, created_at: Date.now() / 1000, updated_at: Date.now() / 1000 },
  { id: 'mem-eino', content: '优先使用 Eino/compose 能力减少自研组件和维护负担。', scope: 'project', modality: 'insight', status: 'active', importance: 1.4, created_at: Date.now() / 1000, updated_at: Date.now() / 1000 },
  { id: 'mem-ui-qa', content: '窄屏下需要重点检查 topbar、A2UI 卡片、Workflow 编辑器与右栏挤压。', scope: 'session', modality: 'episode', status: 'active', importance: 1.0, created_at: Date.now() / 1000, updated_at: Date.now() / 1000 },
]

function withMockMemoryDefaults(items: any[]) {
  return (Array.isArray(items) ? items : []).map((m: any, i: number) => ({
    recall_count: i === 0 ? 3 : i === 1 ? 1 : 0,
    is_canonical: i === 0,
    source_trust: 1,
    supersedes_id: '',
    contradicted_by: '',
    ...m,
  }))
}

function getMockMemories() {
  return withMockMemoryDefaults(readJSON('agentgo-preview-memories', mockMemories))
}

function saveMockMemories(items: any[]) {
  writeJSON('agentgo-preview-memories', items)
}

function baseMockApps() {
  return [
    { id: 'app:dataset-profiler', name: 'dataset-profiler', icon: '▦', description: '分析 CSV/JSON 数据概况并产出指标卡。', enabled: true, kind: 'ui', has_ui: true, bundle_path: 'dataset-profiler' },
    { id: 'app:diff-reviewer', name: 'diff-reviewer', icon: '∆', description: '解释代码 diff 和潜在影响。', enabled: true, kind: 'tool', has_ui: false },
    { id: 'app:workflow-runner', name: 'workflow-runner', icon: '↯', description: '触发并观察工作流运行。', enabled: false, kind: 'ui', has_ui: true, bundle_path: 'workflow-runner' },
  ]
}

function getMockApps() {
  const custom = readJSON<any[]>('agentgo-preview-inner-apps', [])
  return [...baseMockApps(), ...(Array.isArray(custom) ? custom : [])]
}

function mockAppManifest(app: any) {
  const exportsList = Array.isArray(app?.exports) ? app.exports : []
  const actions: any[] = [{ name: 'info', description: 'Read app metadata', source: 'builtin' }]
  const add = (name: string, source = 'derived', binding = '') => {
    if (!name || actions.some(a => a.name === name)) return
    actions.push({ name, description: name === 'ping' ? 'Health check' : name === 'echo' ? 'Echo payload' : 'App action', source, binding })
  }
  if (exportsList.length) {
    exportsList.forEach((name: string) => add(name, 'export', name === 'workflow_run' ? app.workflow_id || '' : ''))
  } else {
    add('ping', 'builtin')
    add('echo', 'builtin')
    if (app?.workflow_id) {
      add('workflow_run', 'derived', app.workflow_id)
      add('run', 'derived', app.workflow_id)
    }
  }
  return {
    name: app?.name || app?.id || 'inner-app',
    description: app?.description || '',
    kind: app?.kind || 'ui',
    enabled: app?.enabled !== false,
    has_ui: !!(app?.has_ui || app?.bundle_path || app?.kind === 'ui'),
    bundle_path: app?.bundle_path || '',
    workflow_id: app?.workflow_id || '',
    exports: exportsList,
    export_policy: exportsList.length ? 'exports' : 'open',
    actions,
  }
}

function findMockApp(name: any) {
  const n = String(name || '')
  return getMockApps().find(a => a.name === n || a.id === n || String(a.id || '').replace(/^app:/, '') === n)
}

function saveCustomMockApp(app: any) {
  const custom = readJSON<any[]>('agentgo-preview-inner-apps', [])
  const next = custom.filter(a => a.name !== app.name)
  next.unshift(app)
  writeJSON('agentgo-preview-inner-apps', next)
}

function mockInnerAppHTML(appName: string) {
  const safeName = String(appName || 'inner-app').replace(/[<>&"]/g, '')
  return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <style>
    * { box-sizing: border-box; }
    body { margin: 0; font-family: Inter, system-ui, sans-serif; background: #10131a; color: #e7eaf0; }
    main { min-height: 100vh; padding: 24px; display: grid; gap: 16px; grid-template-columns: minmax(0, 1fr) 220px; }
    h1 { margin: 0; font-size: 22px; }
    p { color: #a8b0bf; line-height: 1.6; }
    section, aside { border: 1px solid #2b3444; border-radius: 10px; background: #171c26; padding: 16px; }
    button { border: 0; border-radius: 8px; padding: 10px 12px; background: #c2a15b; color: #15120b; font-weight: 700; cursor: pointer; }
    pre { white-space: pre-wrap; background: #0c0f15; border-radius: 8px; padding: 12px; color: #d4dded; min-height: 96px; }
    .metric { display: grid; gap: 8px; margin-top: 12px; }
    .metric div { display: flex; justify-content: space-between; border-bottom: 1px solid #2b3444; padding-bottom: 8px; }
    @media (max-width: 680px) { main { grid-template-columns: 1fr; } }
  </style>
</head>
<body>
  <main>
    <section>
      <h1>${safeName}</h1>
      <p>这是 localhost preview 的 Inner App UI。桌面版会从 app bundle 读取 index.html，并通过 agentGo helpers 调用宿主。</p>
      <button id="run">调用宿主 API</button>
      <pre id="out">等待调用…</pre>
    </section>
    <aside>
      <strong>App Surface</strong>
      <div class="metric">
        <div><span>kind</span><b>ui</b></div>
        <div><span>bundle</span><b>ready</b></div>
        <div><span>mode</span><b>preview</b></div>
      </div>
    </aside>
  </main>
  <script>
    document.getElementById('run').onclick = async () => {
      if (window.agentGo) {
        const out = document.getElementById('out');
        out.textContent = 'calling host...';
        try {
          const r = await window.agentGo.apiCall('ping', { app: '${safeName}', t: Date.now() });
          out.textContent = JSON.stringify(r, null, 2);
        } catch (e) {
          out.textContent = e.message;
        }
        return;
      }
      document.getElementById('out').textContent = JSON.stringify({
        success: true,
        app: '${safeName}',
        note: '真实 Wails 中这里会走 CallInnerAppAPI / InvokeInnerApp'
      }, null, 2)
    }
  </script>
</body>
</html>`
}

const previewFlowgram = {
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

async function mockStream(sessionID: string, text: string) {
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

async function mockCall(method: string, ...params: any[]): Promise<any> {
  logPreviewOnce()
  switch (method) {
    case 'ListSessions':
      return publicSessions()
    case 'NewSession': {
      const title = String(params[0] || '新对话')
      const session: MockSession = { id: `preview-${Date.now()}`, title, updated_at: nowISO(), message_count: 0, messages: [] }
      const sessions = getMockSessions()
      sessions.unshift(session)
      saveMockSessions(sessions)
      return { success: true, id: session.id, session }
    }
    case 'DeleteSession': {
      const sid = String(params[0] || '')
      saveMockSessions(getMockSessions().filter(s => s.id !== sid))
      return { success: true, id: sid }
    }
    case 'GetSessionMessages': {
      const sid = String(params[0] || '')
      const session = ensureSession(sid)
      return { session_id: sid, messages: clone(session.messages), count: session.messages.length }
    }
    case 'SendMessageStream':
      return mockStream(String(params[0] || ''), String(params[1] || ''))
    case 'StopSession':
    case 'CancelA2UIInteraction':
    case 'ResolveA2UIInteraction':
    case 'ResolveApproval':
      return { success: true, preview: true }
    case 'AnswerQuestion':
      return { success: true, content: '预览模式已记录你的回答，真实桌面运行时会继续恢复 Agent。' }

    case 'GetFeatureFlags':
      return { success: true, mode_profile: defaultMode().profile, execution_canvas: defaultMode().canvas, a2ui: true, preview: true }
    case 'GetSessionModeForSession':
      return { success: true, ...getSessionMode(String(params[0] || '')) }
    case 'SetSessionModeForSession': {
      const sid = String(params[0] || '')
      const mode = { profile: String(params[1] || 'assistant'), canvas: String(params[2] || 'balanced') }
      writeJSON(modeKey(sid), mode)
      return { success: true, ...mode }
    }
    case 'SetSessionMode': {
      const mode = { profile: String(params[0] || 'assistant'), canvas: String(params[1] || 'balanced') }
      writeJSON(PREVIEW_GLOBAL_MODE_KEY, mode)
      return { success: true, ...mode }
    }

    case 'GetLLMConfig':
      return readJSON(PREVIEW_LLM_KEY, { api_base: 'https://api.openai.com/v1', api_key: '', model: 'gpt-4o', fallback_model: '' })
    case 'SetLLMConfig': {
      const cfg = { api_base: params[0] || '', api_key: params[1] || '', model: params[2] || '', fallback_model: params[3] || '' }
      writeJSON(PREVIEW_LLM_KEY, cfg)
      return { success: true, preview: true }
    }
    case 'TestLLM':
      return { ok: true, message: 'localhost preview runtime 可用' }

    case 'GetWorkspaceInfo':
      return mockWorkspaceInfo()
    case 'SetWorkspaceRoot': {
      const root = String(params[0] || '').trim()
      if (!root) return { success: false, error: 'workspace root is empty', preview: true }
      writeJSON(PREVIEW_WORKSPACE_ROOT_KEY, root)
      return mockWorkspaceInfo(root)
    }
    case 'ListWorkspace': {
      const rel = cleanRel(params[0])
      return clone(mockWorkspace[rel] || [])
    }
    case 'ReadWorkspaceFile': {
      const rel = cleanRel(params[0])
      const entry = Object.values(mockWorkspace).flat().find((x: any) => x.path === rel)
      if (entry?.size > MAX_PREVIEW_BYTES) {
        return { success: false, error: `文件超过预览限制 (${Math.round(MAX_PREVIEW_BYTES / 1024)} KB)`, size: entry.size }
      }
      const content = mockFiles[rel]
      if (content == null) return { success: false, error: 'mock 文件不存在' }
      return { success: true, content, size: bytesOf(content) }
    }
    case 'WorkspaceFileDiff': {
      const rel = cleanRel(params[0])
      const diff = rel
        ? `diff --git a/${rel} b/${rel}
--- a/${rel}
+++ b/${rel}
@@ -1,3 +1,4 @@
 原始内容
+localhost preview: 新增 Agent 上下文入口
 保留真实 Wails v3 runtime
`
        : ''
      return { success: true, path: rel, diff, added: diff ? 1 : 0, removed: 0, untracked: false, has_changes: !!diff }
    }

    case 'ListMemories':
      return getMockMemories().filter((m: any) => m.status === 'active').slice(0, Number(params[0] || 40))
    case 'MemoryFeedback': {
      const id = String(params[0] || '')
      const signal = String(params[1] || '')
      const items = getMockMemories().map((m: any) => {
        if (m.id !== id) return m
        if (signal === 'disproved') return { ...m, status: 'forgotten', updated_at: Date.now() / 1000 }
        const delta = signal === 'positive' ? 0.15 : signal === 'negative' ? -0.15 : 0
        return { ...m, importance: Math.max(0.1, Math.min(2, Number(m.importance || 1) + delta)), updated_at: Date.now() / 1000 }
      })
      saveMockMemories(items)
      return { success: true, preview: true }
    }
    case 'MemoryGC':
      return { success: true, archived: 1, preview: true }
    case 'MemoryDistill':
    case 'MemoryDistillLLM':
      return { success: true, summary: '预览蒸馏：保留用户对 Eino 复用、UI 可维护面板、Workspace 上下文入口的偏好。', preview: true }
    case 'MemoryContextPrompt':
      return {
        success: true,
        scope: params[0] || 'global',
        prompt: '- 用户偏好：优先复用 Eino 和 Wails v3 真实能力。\n- 当前关注：Agent Context、InnerApp 独立窗口、Memory 可维护面板。',
        preview: true,
      }

    case 'MemoryRecallQA': {
      const q = String(params[0] || 'session context user preferences goals')
      const scope = String(params[1] || 'global')
      const modality = String(params[2] || '')
      const limit = Number(params[3] || 8)
      const rows = getMockMemories()
        .filter((m: any) => m.status === 'active')
        .filter((m: any) => !scope || scope === 'global' || m.scope === scope || m.scope === 'global' || (scope.startsWith('preview') && m.scope === 'session'))
        .filter((m: any) => !modality || m.modality === modality)
        .slice(0, limit)
        .map((m: any, i: number) => ({
          ...m,
          rank: i + 1,
          estimated_score: Number((0.85 - i * 0.07).toFixed(3)),
          source: 'mock_hybrid_recall',
          why: `rank #${i + 1}; scope=${m.scope}; modality=${m.modality}; preview runtime`,
        }))
      return {
        success: true,
        query: q,
        scope,
        modality,
        limit,
        elapsed_ms: 12,
        count: rows.length,
        records: rows,
        engine: 'localhost-preview',
        prompt: rows.map((r: any) => `- [${r.modality}] ${r.content}`).join('\n'),
        prompt_bytes: rows.reduce((n: number, r: any) => n + String(r.content || '').length, 0),
        preview: true,
      }
    }

    case 'DetectMemoryContradictions': {
      const newFact = String(params[0] || '')
      const scope = String(params[1] || 'global')
      const candidate = getMockMemories()
        .find((m: any) => m.status === 'active' && (!scope || scope === 'global' || m.scope === scope || m.scope === 'global'))
      return candidate ? [{
        existing_id: candidate.id,
        explanation: `preview candidate for "${newFact.slice(0, 40)}"`,
      }] : []
    }
    case 'ResolveMemoryTruth':
      return {
        resolution: 'supersede_a_with_b',
        explanation: 'localhost preview: treat Fact B as newer/canonical',
      }
    case 'ApplyMemoryTruthResolution': {
      const factAID = String(params[0] || '')
      const factBID = String(params[1] || '')
      let resolution: any = {}
      try { resolution = JSON.parse(String(params[2] || '{}')) } catch {}
      const items = getMockMemories()
      let applied: any = null
      const next = items.map((m: any) => {
        if (resolution.resolution === 'supersede_a_with_b' && m.id === factAID) {
          return { ...m, status: 'archived', supersedes_id: factBID, contradicted_by: factBID, updated_at: Date.now() / 1000 }
        }
        if (resolution.resolution === 'supersede_a_with_b' && m.id === factBID) {
          applied = { ...m, is_canonical: true, updated_at: Date.now() / 1000 }
          return applied
        }
        if (resolution.resolution === 'supersede_b_with_a' && m.id === factBID) {
          return { ...m, status: 'archived', supersedes_id: factAID, contradicted_by: factAID, updated_at: Date.now() / 1000 }
        }
        if (resolution.resolution === 'supersede_b_with_a' && m.id === factAID) {
          applied = { ...m, is_canonical: true, updated_at: Date.now() / 1000 }
          return applied
        }
        return m
      })
      if (resolution.resolution === 'merge') {
        applied = {
          id: `truth-merge-${Date.now()}`,
          content: resolution.merged_content || 'Preview merged memory',
          scope: 'global',
          modality: 'fact',
          status: 'active',
          importance: 1.4,
          recall_count: 0,
          is_canonical: true,
          source_trust: 1,
          created_at: Date.now() / 1000,
          updated_at: Date.now() / 1000,
        }
        next.unshift(applied)
      }
      saveMockMemories(next)
      resolution.applied_record_id = applied?.id || factBID || factAID
      return { success: true, resolution, record: applied || next.find((m: any) => m.id === resolution.applied_record_id), preview: true }
    }
    case 'ResolveAndApplyMemoryTruth': {
      const res = await mockCall('ResolveMemoryTruth', params[0], params[1])
      return mockCall('ApplyMemoryTruthResolution', params[0], params[1], JSON.stringify(res))
    }
    case 'GetMemoryGraph': {
      const center = String(params[0] || '')
      const rows = getMockMemories().filter((m: any) => m.status === 'active')
      const centerRow = rows.find((m: any) => m.id === center) || rows[0]
      const neighbors = rows.filter((m: any) => m.id !== centerRow?.id).slice(0, 3)
      return {
        success: !!centerRow,
        graph: {
          nodes: centerRow ? [centerRow, ...neighbors] : [],
          edges: centerRow ? neighbors.map((n: any, i: number) => ({
            source_id: centerRow.id,
            target_id: n.id,
            relation: i === 0 ? 'related' : 'supports',
            weight: 1 - i * 0.1,
          })) : [],
        },
        preview: true,
      }
    }

    case 'ListSkills':
      return [
        { id: 'skill:code_review', name: 'code_review', description: '检查行为回归、风险和测试缺口。', scope: 'project', path: 'skills/code_review.md' },
        { id: 'skill:frontend_qa', name: 'frontend_qa', description: '做响应式布局和交互状态 QA。', scope: 'workspace', path: 'skills/frontend_qa.md' },
        { id: 'skill:eino_architecture', name: 'eino_architecture', description: '优先复用 Eino compose、tool 和 callback 能力。', scope: 'project', path: 'skills/eino_architecture.md' },
      ]
    case 'GetSkillContext':
      return `# Skill Context

- 使用 Eino/compose 能力减少自研调度代码。
- UI QA 时检查 topbar、A2UI 卡片和 Workflow 编辑器在右栏打开时的布局。`
    case 'ListRegisteredTools':
      return ['list_workspace', 'read_file', 'workspace_file_diff', 'remember', 'invoke_inner_app', 'run_workflow']
    case 'ListCapabilityGrants': {
      const kind = String(params[0] || '')
      const rows = mockCapabilityGrants()
      return kind ? rows.filter(g => g.kind === kind) : rows
    }
    case 'ListCapabilities': {
      const kind = String(params[0] || '')
      const rows = mockCapabilityGrants()
      return (kind ? rows.filter(g => g.kind === kind) : rows).map(g => ({ id: g.id, kind: g.kind, name: g.name, scope: g.scope, metadata: g.metadata }))
    }
    case 'TransitionCapabilityGrant':
    case 'VerifyCapabilityGrant':
    case 'DeprecateCapabilityGrant':
    case 'DeleteCapabilityGrant':
      return { success: true, preview: true }

    case 'ListInnerApps':
      return getMockApps().map(app => ({ ...app, manifest: mockAppManifest(app) }))
    case 'BuildInnerAppIteratively': {
      const name = String(params[0] || '').trim() || `preview-app-${Date.now()}`
      const app = {
        id: `app:${name}`,
        name,
        icon: '▣',
        description: String(params[2] || params[1] || 'AI 生成的预览 Inner App'),
        enabled: true,
        kind: 'ui',
        has_ui: true,
        bundle_path: name,
      }
      saveCustomMockApp(app)
      return {
        success: true,
        app_name: name,
        iterations: 1,
        message: 'localhost preview 已生成 UI bundle',
        final_verify: { success: true, score: 92, max_score: 100, message: 'preview verification passed' },
      }
    }
    case 'GetInnerAppInfo': {
      const app = getMockApps().find(a => a.name === params[0] || a.id === params[0])
      return app ? { success: true, ...app, manifest: mockAppManifest(app), files: ['index.html', 'static/app.js', 'static/style.css'] } : { success: false, error: 'app not found' }
    }
    case 'GetInnerAppManifest': {
      const app = findMockApp(params[0])
      return app ? { success: true, manifest: mockAppManifest(app) } : { success: false, error: 'app not found' }
    }
    case 'OpenInnerAppSession': {
      const app = findMockApp(params[0]) || { name: params[0] || 'inner-app', kind: 'ui', enabled: true, has_ui: true }
      return {
        success: true,
        session_id: `preview-inner-${Date.now()}`,
        nonce: Math.random().toString(36).slice(2),
        created_at: Math.floor(Date.now() / 1000),
        expires_at: Math.floor(Date.now() / 1000) + 7200,
        ttl_sec: 7200,
        manifest: mockAppManifest(app),
        preview: true,
      }
    }
    case 'GetInnerAppPageHTML':
      return { success: true, html: mockInnerAppHTML(String(params[0] || 'inner-app')), app: params[0] || 'inner-app' }
    case 'OpenInnerAppWindow':
      return { success: false, error: 'localhost preview 使用内嵌预览，Wails 桌面版会打开独立窗口。', preview: true }
    case 'ReadInnerAppFile':
      return { success: true, text: '', mime: 'text/plain; charset=utf-8' }
    case 'InvokeInnerApp':
      return { success: true, content: `预览模式已模拟调用 ${params[0] || 'app'}` }
    case 'CallInnerAppAPI':
      return { success: true, output: `预览模式已调用 ${params[0] || 'app'}.${params[1] || 'action'}`, payload: params[2] || '{}' }

    case 'CallInnerAppSessionAPI':
      return { success: true, output: `preview called ${params[0] || 'app'}.${params[3] || 'action'}`, session_id: params[1] || '', payload: params[4] || '{}', preview: true }

    case 'ListBackgroundTasks':
      return [
        { id: 'task-ui-qa', title: 'UI 视觉 QA', goal: '检查窄屏与右栏打开状态', status: 'running', steps: 4 },
        { id: 'task-eino-gap', title: 'Eino 差距梳理', goal: '减少自研组件', status: 'completed', steps: 6 },
      ]
    case 'ListScheduledJobs':
      return [{ id: 'job-memory-gc', title: 'Memory GC', spec: 'daily 03:00', status: 'active' }]
    case 'ListChannels':
      return [
        { id: 'local', channel: 'Local Desktop', kind: 'desktop', state: 'connected' },
        { id: 'preview', channel: 'Vite Preview', kind: 'browser', state: 'active' },
      ]

    case 'ListWorkflows':
      return [{ id: 'wf_preview_happy_path', name: '预览 Happy Path', description: 'Start → Tool → End' }]
    case 'GetWorkflowFlowgram':
      return { success: true, flowgram: clone(previewFlowgram) }
    case 'SaveWorkflowFlowgram':
    case 'SaveWorkflow':
      return { success: true, preview: true }
    case 'RunWorkflow':
      return { success: true, output: '预览工作流运行完成。' }
    case 'EnsureWorkflowHappyPath':
      return { success: true, id: 'wf_preview_happy_path', message: '预览演示工作流已就绪。' }
    case 'OpenWorkflowWindow':
      return { success: false, error: 'localhost preview 使用页内弹出编辑器，Wails 桌面版会打开独立窗口。', preview: true }

    default:
      console.warn('[AgentGo] mock runtime 未实现方法:', method, params)
      return { success: true, preview: true }
  }
}

export async function wailsCall(method: string, ...params: any[]): Promise<any> {
  const task = async () => {
    const rt = await loadRuntime(2500)
    if (rt?.Call?.ByName) {
      const full = `${SVC}.${method}`
      console.info('[AgentGo] IPC →', full)
      const ret = rt.Call.ByName(full, ...params)
      return ret && typeof (ret as any).then === 'function' ? await ret : ret
    }
    return mockCall(method, ...params)
  }
  const next = ipcQueue.then(task, task)
  ipcQueue = next.catch(() => {}) as any
  return next
}

export async function wailsCallUrgent(method: string, timeoutMs: number, ...params: any[]): Promise<any> {
  const rt = await loadRuntime(Math.min(timeoutMs, 2500))
  if (rt?.Call?.ByName) {
    const full = `${SVC}.${method}`
    let timer: ReturnType<typeof setTimeout>
    const timeout = new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error(`${method} 超时 (${timeoutMs}ms)`)), timeoutMs)
    })
    try {
      const ret = rt.Call.ByName(full, ...params)
      const result = ret && typeof (ret as any).then === 'function' ? await Promise.race([ret, timeout]) : ret
      clearTimeout(timer!)
      return result
    } catch (e) {
      clearTimeout(timer!)
      throw e
    }
  }
  return Promise.race([
    mockCall(method, ...params),
    new Promise<never>((_, reject) => setTimeout(() => reject(new Error(`${method} 超时 (${timeoutMs}ms)`)), timeoutMs)),
  ])
}

export async function wailsCallWithTimeout(method: string, timeoutMs: number, ...params: any[]): Promise<any> {
  let timer: ReturnType<typeof setTimeout>
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error(`${method} 超时 (${timeoutMs}ms)`)), timeoutMs)
  })
  try {
    return await Promise.race([wailsCall(method, ...params), timeout])
  } finally {
    clearTimeout(timer!)
  }
}
