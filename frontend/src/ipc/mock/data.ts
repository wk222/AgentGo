// 由 wails.ts 机械拆分而来（见 docs/plan.md F6）。
import { readJSON, writeJSON } from './state'

export function mockCapabilityGrants() {
  return [
    { id: 'tool:execute_bash:agent', kind: 'tool', name: 'execute_bash', scope: 'agent', status: 'published', risk_level: 'high', reusable: true, recommended: false, verify_result: 'pass', metadata: { risk: 'high' } },
    { id: 'tool:list_workspace_dir:agent', kind: 'tool', name: 'list_workspace_dir', scope: 'agent', status: 'published', risk_level: 'low', reusable: true, recommended: true, verify_result: 'pass', metadata: { workspace: 'locked' } },
    { id: 'workflow:wf_preview_happy_path:global', kind: 'workflow', name: 'wf_preview_happy_path', scope: 'global', status: 'published', risk_level: 'medium', reusable: true, recommended: true, verify_result: 'skip', metadata: { source: 'preview' } },
    { id: 'app:dataset-profiler:inner', kind: 'app', name: 'dataset-profiler', scope: 'inner', status: 'published', risk_level: 'low', reusable: true, recommended: true, verify_result: 'pass', metadata: { kind: 'ui' } },
    { id: 'skill:eino_architecture:project', kind: 'skill', name: 'eino_architecture', scope: 'project', status: 'verified', risk_level: 'low', reusable: true, recommended: true, verify_result: 'pass', metadata: { path: 'skills/eino_architecture.md' } },
    { id: 'channel:preview:browser', kind: 'channel', name: 'preview', scope: 'browser', status: 'published', risk_level: 'low', reusable: false, recommended: false, verify_result: 'pass', metadata: { state: 'active' } },
  ]
}

export const mockMemories = [
  { id: 'mem-ui-context', content: '用户希望右侧 Workspace 成为 Agent 上下文入口。', scope: 'agent', modality: 'fact', status: 'active', importance: 1.2, created_at: Date.now() / 1000, updated_at: Date.now() / 1000 },
  { id: 'mem-eino', content: '优先使用 Eino/compose 能力减少自研组件和维护负担。', scope: 'project', modality: 'insight', status: 'active', importance: 1.4, created_at: Date.now() / 1000, updated_at: Date.now() / 1000 },
  { id: 'mem-ui-qa', content: '窄屏下需要重点检查 topbar、A2UI 卡片、Workflow 编辑器与右栏挤压。', scope: 'session', modality: 'episode', status: 'active', importance: 1.0, created_at: Date.now() / 1000, updated_at: Date.now() / 1000 },
]

interface MockTerminalSession {
  info: {
    id: string
    title: string
    pid: number
    shell: string
    cwd: string
    created_at: string
  }
  cols: number
  rows: number
}
export const mockTerminals = new Map<string, MockTerminalSession>()
export const mockTermInput = new Map<string, string>()

export function withMockMemoryDefaults(items: any[]) {
  return (Array.isArray(items) ? items : []).map((m: any, i: number) => ({
    recall_count: i === 0 ? 3 : i === 1 ? 1 : 0,
    is_canonical: i === 0,
    source_trust: 1,
    supersedes_id: '',
    contradicted_by: '',
    ...m,
  }))
}

export function getMockMemories() {
  return withMockMemoryDefaults(readJSON('agentgo-preview-memories', mockMemories))
}

export function saveMockMemories(items: any[]) {
  writeJSON('agentgo-preview-memories', items)
}

export function baseMockApps() {
  return [
    { id: 'app:dataset-profiler', name: 'dataset-profiler', icon: '▦', description: '分析 CSV/JSON 数据概况并产出指标卡。', enabled: true, kind: 'ui', has_ui: true, bundle_path: 'dataset-profiler' },
    { id: 'app:diff-reviewer', name: 'diff-reviewer', icon: '∆', description: '解释代码 diff 和潜在影响。', enabled: true, kind: 'tool', has_ui: false },
    { id: 'app:workflow-runner', name: 'workflow-runner', icon: '↯', description: '触发并观察工作流运行。', enabled: false, kind: 'ui', has_ui: true, bundle_path: 'workflow-runner' },
  ]
}

export function getMockApps() {
  const custom = readJSON<any[]>('agentgo-preview-inner-apps', [])
  return [...baseMockApps(), ...(Array.isArray(custom) ? custom : [])]
}

export function mockAppManifest(app: any) {
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

export function findMockApp(name: any) {
  const n = String(name || '')
  return getMockApps().find(a => a.name === n || a.id === n || String(a.id || '').replace(/^app:/, '') === n)
}

export function saveCustomMockApp(app: any) {
  const custom = readJSON<any[]>('agentgo-preview-inner-apps', [])
  const next = custom.filter(a => a.name !== app.name)
  next.unshift(app)
  writeJSON('agentgo-preview-inner-apps', next)
}

export function mockInnerAppHTML(appName: string) {
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
